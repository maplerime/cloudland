"""Volume cleanup logic for CloudLand Cleaner.

Identifies and removes zombie WDS volumes that have been soft-deleted
in the CloudLand database but still exist in WDS storage.
Supports incremental checkpoint scanning: progress is saved after each
volume so a subsequent run can resume from where it left off.
"""

import logging

from rich.progress import Progress, BarColumn, TextColumn, MofNCompleteColumn, TimeElapsedColumn
from rich.table import Table
from rich.console import Console

from cloudland_cli.db import get_deleted_volumes, count_deleted_volumes
from cloudland_cli.cache import save_checkpoint, load_checkpoint, clear_cache

logger = logging.getLogger(__name__)
console = Console()

CACHE_PREFIX = "clean_volumes"


def _cache_name(vol_type):
    """Build cache key name for a volume type.

    Args:
        vol_type: 'all', 'boot', or 'data'.

    Returns:
        Cache key string.
    """
    return f"{CACHE_PREFIX}_{vol_type}"


def _parse_wds_volume_id(path):
    """Extract the WDS volume ID from a volume path.

    Path format: wds_vhost://<pool-id>/<volume-id>
    or           wds_iscsi://<pool-id>/<volume-id>

    Args:
        path: Volume path string.

    Returns:
        WDS volume ID string, or None if path cannot be parsed.
    """
    if not path:
        return None
    parts = path.split("://", 1)
    if len(parts) != 2:
        return None
    segments = parts[1].split("/")
    # Expect pool-id/volume-id
    if len(segments) == 2:
        return segments[1]
    return None


def _scan_zombie_volumes(db_params, wds_client, vol_type, cache_key):
    """Scan DB and WDS to find zombie volumes with checkpoint support.

    Loads any existing checkpoint to resume from the last checked ID.
    Saves checkpoint after each volume so progress is never lost.

    Args:
        db_params: Database connection parameters.
        wds_client: Authenticated WDSClient instance.
        vol_type: 'all', 'boot', or 'data'.
        cache_key: Cache key for saving checkpoints.

    Returns:
        Tuple of (total_db_count, zombies list, skipped count, checked_count, scan_complete bool).
    """
    # Load existing checkpoint
    ckpt = load_checkpoint(cache_key)
    last_checked_id = 0
    zombies = []
    prev_checked = 0

    if ckpt is not None:
        last_checked_id = ckpt.get("last_checked_id", 0)
        zombies = ckpt.get("zombies", [])
        prev_checked = ckpt.get("checked_count", 0)
        console.print(
            f"[bold green]Resuming from checkpoint[/] "
            f"(last_checked_id={last_checked_id}, "
            f"checked={prev_checked}, "
            f"zombie={len(zombies)}, "
            f"saved at {ckpt.get('updated_at', '?')})"
        )

    # Get total count for progress display
    total_db = count_deleted_volumes(db_params, vol_type)

    # Query remaining volumes after checkpoint
    volumes = get_deleted_volumes(db_params, vol_type, start_after_id=last_checked_id)
    logger.info("Total soft-deleted: %d, remaining to check: %d", total_db, len(volumes))

    if not volumes and not zombies:
        return total_db, [], 0, prev_checked, True

    if not volumes:
        # All checked in previous runs, scan is complete
        console.print(f"All {total_db} volumes already checked.")
        return total_db, zombies, 0, prev_checked, True

    skipped = 0
    checked_count = prev_checked

    # Use total_db for progress so the bar reflects overall progress across runs
    with Progress(
        TextColumn("[bold blue]Scanning volumes"),
        BarColumn(),
        MofNCompleteColumn(),
        TextColumn("[yellow]zombie:{task.fields[zombie]}[/]"),
        TextColumn("[dim]skip:{task.fields[skipped]}[/]"),
        TimeElapsedColumn(),
        console=console,
    ) as progress:
        task = progress.add_task(
            "scan", total=total_db,
            completed=prev_checked,
            zombie=len(zombies), skipped=0,
        )
        for vol in volumes:
            wds_id = _parse_wds_volume_id(vol["path"])
            if not wds_id:
                logger.warning("Cannot parse WDS volume ID from path: %s (volume id=%d)", vol["path"], vol["id"])
                skipped += 1
                checked_count += 1
                last_checked_id = vol["id"]
                progress.update(task, advance=1, skipped=skipped)
                # Save checkpoint periodically (every volume)
                save_checkpoint(cache_key, last_checked_id, zombies, total_db, checked_count)
                continue

            try:
                exists = wds_client.volume_exists(wds_id)
            except Exception as e:
                logger.error("Error checking volume %s: %s", wds_id, e)
                skipped += 1
                checked_count += 1
                last_checked_id = vol["id"]
                progress.update(task, advance=1, skipped=skipped)
                save_checkpoint(cache_key, last_checked_id, zombies, total_db, checked_count)
                continue

            if exists:
                zombies.append({**vol, "wds_id": wds_id})

            checked_count += 1
            last_checked_id = vol["id"]
            progress.update(task, advance=1, zombie=len(zombies), skipped=skipped)
            # Save checkpoint after each volume
            save_checkpoint(cache_key, last_checked_id, zombies, total_db, checked_count)

    return total_db, zombies, skipped, checked_count, True


def _print_zombie_table(zombies, wds_client):
    """Print a rich table of zombie volumes to console and logger.

    Args:
        zombies: List of zombie volume dicts.
        wds_client: WDSClient instance to fetch volume details.
    """
    table = Table(title="Zombie Volumes", show_lines=True)
    table.add_column("DB ID")
    table.add_column("UUID")
    table.add_column("Name")
    table.add_column("Type")
    table.add_column("Status")
    table.add_column("WDS Volume ID")
    table.add_column("Deleted At")
    table.add_column("Size (GB)")

    total_size_bytes = 0

    for z in zombies:
        vol_name = z.get("name", "")
        wds_id = z["wds_id"]

        # Get volume detail to fetch data_size
        size_gb = 0.0
        try:
            vol_detail = wds_client.get_volume_detail(wds_id)
            if vol_detail and "data_size" in vol_detail:
                size_bytes = vol_detail.get("data_size", 0)
                size_gb = size_bytes / (1024 ** 3)  # Convert bytes to GB
                total_size_bytes += size_bytes
        except Exception as e:
            logger.debug("Failed to get volume detail for %s: %s", wds_id, e)

        table.add_row(
            str(z["id"]),
            z.get("uuid", ""),
            vol_name,
            "boot" if z.get("booting") else "data",
            str(z.get("status", "")),
            z["wds_id"],
            str(z.get("deleted_at", "")),
            f"{size_gb:.2f}",
        )

    console.print(table)

    # Also log the table as text to logger
    import io
    text_console = Console(file=io.StringIO(), width=120, force_terminal=False)
    text_console.print(table)
    table_text = text_console.file.getvalue()

    # Log each line of the table
    for line in table_text.split('\n'):
        if line.strip():
            logger.info(line)

    # Return total size for summary
    total_size_gb = total_size_bytes / (1024 ** 3)
    return total_size_gb


def _delete_zombies(wds_client, zombies, execute=False):
    """Delete zombie volumes from WDS with progress bar.

    Before deleting a volume, unbind any associated vhosts and USS servers.

    Args:
        wds_client: Authenticated WDSClient instance.
        zombies: List of zombie volume dicts with 'wds_id' key.
        execute: If True, actually delete; if False, dry-run mode.

    Returns:
        Tuple of (cleaned count, failed count).
    """
    cleaned = 0
    failed = 0
    mode = "[bold red]EXECUTE[/]" if execute else "[bold yellow]DRY-RUN[/]"

    with Progress(
        TextColumn("[bold red]Deleting volumes"),
        BarColumn(),
        MofNCompleteColumn(),
        TextColumn("[green]ok:{task.fields[cleaned]}[/]"),
        TextColumn("[red]fail:{task.fields[failed]}[/]"),
        TimeElapsedColumn(),
        console=console,
    ) as progress:
        task = progress.add_task(
            "delete", total=len(zombies), cleaned=0, failed=0
        )
        for z in zombies:
            wds_id = z["wds_id"]
            db_id = z.get("id", "?")
            vol_name = z.get("name", "?")

            # Display current volume being processed with mode indicator
            console.print(f"[cyan]Processing:[/] {vol_name} (WDS ID: {wds_id}) [{mode}]")

            try:
                # Step 1: Get vhosts bound to this volume
                vhosts = wds_client.get_volume_vhosts(wds_id)
                logger.debug("Volume %s has %d vhost(s)", wds_id, len(vhosts))

                # Log details of each vhost
                if vhosts:
                    for vhost in vhosts:
                        vhost_id = vhost.get("id", "")
                        vhost_name = vhost.get("name", "?")
                        logger.info("Found vhost: name=%s, ID=%s on volume %s", vhost_name, vhost_id, wds_id)

                # Step 2: For each vhost, unbind USS servers and delete vhost
                for vhost in vhosts:
                    vhost_id = vhost.get("id", "")
                    vhost_name = vhost.get("name", "?")

                    # Get USS bound to this vhost
                    uss_list = wds_client.get_vhost_bound_uss(vhost_id)
                    logger.debug("Vhost %s has %d USS(s)", vhost_id, len(uss_list))

                    # Log details of each USS
                    if uss_list:
                        for uss in uss_list:
                            uss_id = uss.get("id", "")
                            uss_name = uss.get("name", "?")
                            logger.info("Found USS: name=%s, ID=%s on vhost %s (%s)", uss_name, uss_id, vhost_name, vhost_id)

                    # Unbind each USS
                    for uss in uss_list:
                        uss_id = uss.get("id", "")
                        uss_name = uss.get("name", "?")
                        try:
                            ok, resp = wds_client.unbind_uss(vhost_id, uss_id)
                            if ok:
                                logger.info("Unbound USS %s (%s) from vhost %s", uss_id, uss_name, vhost_id)
                            else:
                                logger.warning("Failed to unbind USS %s from vhost %s: %s", uss_id, vhost_id, resp)
                                console.print(f"[yellow]Warning[/] unbinding USS {uss_name}: {resp}")
                        except Exception as e:
                            logger.warning("Error unbinding USS %s: %s", uss_id, e)
                            console.print(f"[yellow]Warning[/] error unbinding USS {uss_name}: {e}")

                    # Delete vhost
                    try:
                        ok, resp = wds_client.delete_vhost(vhost_id)
                        if ok:
                            logger.info("Deleted vhost %s (%s)", vhost_id, vhost_name)
                        else:
                            logger.warning("Failed to delete vhost %s: %s", vhost_id, resp)
                            console.print(f"[yellow]Warning[/] deleting vhost {vhost_name}: {resp}")
                    except Exception as e:
                        logger.warning("Error deleting vhost %s: %s", vhost_id, e)
                        console.print(f"[yellow]Warning[/] error deleting vhost: {e}")

                # Step 3: Delete the volume
                ok, resp = wds_client.delete_volume(wds_id)
                if ok:
                    cleaned += 1
                    logger.info("Deleted WDS volume %s (DB id=%s)", wds_id, db_id)
                else:
                    failed += 1
                    logger.error("Failed to delete WDS volume %s (DB id=%s): %s", wds_id, db_id, resp)
                    console.print(f"[red]Failed[/] to delete volume {wds_id}: {resp}")
            except Exception as e:
                failed += 1
                logger.error("Error deleting WDS volume %s: %s", wds_id, e)
                console.print(f"[red]Error[/] deleting volume {wds_id}: {e}")

            progress.update(task, advance=1, cleaned=cleaned, failed=failed)
    return cleaned, failed


def clean_volumes(db_params, wds_client, vol_type="all", execute=False, no_cache=False):
    """Scan and optionally clean zombie WDS volumes.

    Scanning is incremental: progress is checkpointed after each volume,
    so the next run resumes from where it left off.

    In execute mode, cached zombie list is used directly for deletion
    (skipping re-scan) unless --no-cache is specified.

    Args:
        db_params: Database connection parameters.
        wds_client: Authenticated WDSClient instance.
        vol_type: 'all', 'boot', or 'data'.
        execute: If True, actually delete zombie volumes; otherwise dry-run.
        no_cache: If True, ignore checkpoint and force re-scan from scratch.

    Returns:
        dict with total, zombie, cleaned, failed counts.
    """
    mode = "EXECUTE" if execute else "DRY-RUN"
    cache_key = _cache_name(vol_type)
    vol_type_label = {"all": "All", "boot": "Boot", "data": "Data"}[vol_type]
    logger.info("Starting volume cleanup (type=%s, mode=%s)", vol_type, mode)

    # Clear checkpoint if --no-cache
    if no_cache:
        clear_cache(cache_key)

    zombies = None
    total = 0
    skipped = 0

    # In execute mode, try using cached zombie list directly
    if execute and not no_cache:
        ckpt = load_checkpoint(cache_key)
        if ckpt is not None and ckpt.get("zombies"):
            total_db = ckpt.get("total_db", 0)
            checked = ckpt.get("checked_count", 0)
            zombies = ckpt["zombies"]
            total = total_db
            console.print(
                f"[bold green]Loaded cached scan results[/] "
                f"(checked {checked}/{total_db}, "
                f"{len(zombies)} zombies, "
                f"saved at {ckpt.get('updated_at', '?')})"
            )

    # Scan if no usable cache
    if zombies is None:
        total, zombies, skipped, _, _ = _scan_zombie_volumes(db_params, wds_client, vol_type, cache_key)

    if total == 0 and not zombies:
        console.print(f"No soft-deleted WDS volumes found (type={vol_type}).")
        return {"total": 0, "zombie": 0, "cleaned": 0, "failed": 0}

    # Report
    report_header = f"\n=== Zombie Volume Report ({vol_type_label}) [{mode}] ==="
    console.print(f"[bold]{report_header}[/]")
    logger.info(report_header)

    report_lines = [
        f"Total in DB:         {total}",
        f"Zombie in WDS:       {len(zombies)}",
    ]
    if skipped:
        report_lines.append(f"Skipped (parse/err): {skipped}")

    for line in report_lines:
        console.print(line)
        logger.info(line)

    if not zombies:
        console.print("No zombie volumes found.")
        return {"total": total, "zombie": 0, "cleaned": 0, "failed": 0}

    total_size_gb = _print_zombie_table(zombies, wds_client)

    if not execute:
        console.print(f"\nDry-run mode: {len(zombies)} volumes would be deleted.")
        console.print(f"Total capacity to be freed: {total_size_gb:.2f} GB")
        console.print("[dim]Progress checkpointed. Next dry-run will resume from last position.[/]")
        console.print("[dim]Run with --execute to delete using cached zombie list.[/]")
        # Also log to file
        logger.info(f"Total capacity to be freed: {total_size_gb:.2f} GB")

    # Execute deletion
    cleaned = 0
    failed = 0
    if execute:
        cleaned, failed = _delete_zombies(wds_client, zombies, execute=True)
        console.print(f"\nResults: cleaned={cleaned}, failed={failed}")
        console.print(f"Capacity freed: {total_size_gb:.2f} GB")
        logger.info(f"Results: cleaned={cleaned}, failed={failed}")
        logger.info(f"Capacity freed: {total_size_gb:.2f} GB")
        # Clear checkpoint after all deletions succeed
        if failed == 0:
            clear_cache(cache_key)

    return {"total": total, "zombie": len(zombies), "cleaned": cleaned, "failed": failed}
