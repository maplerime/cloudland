"""Image snapshot cleanup logic for CloudLand Cleaner.

Identifies and removes zombie WDS image snapshots that have no
remaining clone volumes, meaning they are no longer needed.
"""

import logging

from rich.progress import Progress, BarColumn, TextColumn, MofNCompleteColumn, TimeElapsedColumn
from rich.table import Table
from rich.console import Console

from cloudland_cli.db import get_image_volume_ids
from cloudland_cli.cache import save_cache, load_cache, clear_cache

logger = logging.getLogger(__name__)
console = Console()

CACHE_KEY = "clean_images_snapshot"


def _scan_orphan_snapshots(db_params, wds_client):
    """Scan DB and WDS to find orphan image snapshots.

    Args:
        db_params: Database connection parameters.
        wds_client: Authenticated WDSClient instance.

    Returns:
        Tuple of (total_wds_snapshots, image_snapshot_count, orphans list).
    """
    # Get image volume IDs from DB
    image_vol_ids = get_image_volume_ids(db_params)
    logger.info("Found %d image volume IDs in database", len(image_vol_ids))

    if not image_vol_ids:
        console.print("No image storages found in database.")
        return 0, 0, []

    # List all WDS snapshots
    try:
        console.print("Fetching snapshot list from WDS...")
        all_snapshots = wds_client.list_snapshots()
    except Exception as e:
        logger.error("Failed to list WDS snapshots: %s", e)
        console.print(f"[red]Error listing WDS snapshots: {e}[/]")
        return 0, 0, []

    logger.info("Found %d total snapshots in WDS", len(all_snapshots))

    # Filter snapshots belonging to image volumes
    image_snapshots = [s for s in all_snapshots if s.get("volume_id", "") in image_vol_ids]
    logger.info("Found %d snapshots belonging to image volumes", len(image_snapshots))
    console.print(f"Total WDS snapshots: {len(all_snapshots)}, image volume snapshots: {len(image_snapshots)}")

    if not image_snapshots:
        return len(all_snapshots), 0, []

    # Check each image snapshot for clone volumes with progress bar
    orphans = []
    with Progress(
        TextColumn("[bold blue]Checking clones"),
        BarColumn(),
        MofNCompleteColumn(),
        TextColumn("[yellow]orphan:{task.fields[orphan]}[/]"),
        TimeElapsedColumn(),
        console=console,
    ) as progress:
        task = progress.add_task("scan", total=len(image_snapshots), orphan=0)
        for snap in image_snapshots:
            snap_id = snap.get("id", "")
            try:
                clones = wds_client.get_clone_volumes(snap_id)
            except Exception as e:
                logger.error("Error checking clones for snapshot %s: %s", snap_id, e)
                progress.update(task, advance=1)
                continue

            if not clones:
                orphans.append(snap)
                logger.debug("Orphan snapshot: %s (name=%s, volume_id=%s)", snap_id, snap.get("name", ""), snap.get("volume_id", ""))

            progress.update(task, advance=1, orphan=len(orphans))

    return len(all_snapshots), len(image_snapshots), orphans


def _print_orphan_table(orphans):
    """Print a rich table of orphan snapshots to console and logger.

    Args:
        orphans: List of orphan snapshot dicts.
    """
    table = Table(title="Orphan Image Snapshots", show_lines=True)
    table.add_column("Snapshot ID")
    table.add_column("Name")
    table.add_column("Parent Volume ID")
    table.add_column("Size")
    table.add_column("Created At")
    for snap in orphans:
        table.add_row(
            str(snap.get("id", "")),
            snap.get("name", ""),
            snap.get("volume_id", ""),
            str(snap.get("size", "")),
            str(snap.get("created_at", "")),
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


def _delete_orphans(wds_client, orphans, execute=False):
    """Delete orphan snapshots from WDS with progress bar.

    Args:
        wds_client: Authenticated WDSClient instance.
        orphans: List of orphan snapshot dicts with 'id' key.
        execute: If True, actually delete; if False, dry-run mode.

    Returns:
        Tuple of (cleaned count, failed count).
    """
    cleaned = 0
    failed = 0
    mode = "[bold red]EXECUTE[/]" if execute else "[bold yellow]DRY-RUN[/]"

    with Progress(
        TextColumn("[bold red]Deleting snapshots"),
        BarColumn(),
        MofNCompleteColumn(),
        TextColumn("[green]ok:{task.fields[cleaned]}[/]"),
        TextColumn("[red]fail:{task.fields[failed]}[/]"),
        TimeElapsedColumn(),
        console=console,
    ) as progress:
        task = progress.add_task("delete", total=len(orphans), cleaned=0, failed=0)
        for snap in orphans:
            snap_id = snap.get("id", "")
            snap_name = snap.get("name", "?")

            # Display current snapshot being processed with mode indicator
            console.print(f"[cyan]Processing:[/] {snap_name} (Snapshot ID: {snap_id}) [{mode}]")
            try:
                ok, resp = wds_client.delete_snapshot(snap_id)
                if ok:
                    cleaned += 1
                    logger.info("Deleted WDS snapshot %s (name=%s)", snap_id, snap.get("name", ""))
                else:
                    failed += 1
                    logger.error(
                        "Failed to delete WDS snapshot %s: %s",
                        snap_id,
                        resp
                    )
                    # Also print to console
                    console.print(f"[red]Failed[/] to delete snapshot {snap_id}: {resp}")
            except Exception as e:
                failed += 1
                logger.error("Error deleting WDS snapshot %s: %s", snap_id, e)
                console.print(f"[red]Error[/] deleting snapshot {snap_id}: {e}")
            progress.update(task, advance=1, cleaned=cleaned, failed=failed)
    return cleaned, failed


def clean_image_snapshots(db_params, wds_client, execute=False, no_cache=False):
    """Scan and optionally clean orphan image snapshots in WDS.

    In dry-run mode, scan results are saved to cache.
    In execute mode, cached results are loaded to skip re-scanning
    unless --no-cache is specified.

    Args:
        db_params: Database connection parameters.
        wds_client: Authenticated WDSClient instance.
        execute: If True, actually delete orphan snapshots; otherwise dry-run.
        no_cache: If True, ignore cached results and force re-scan.

    Returns:
        dict with total_snapshots, image_snapshots, orphan, cleaned, failed counts.
    """
    mode = "EXECUTE" if execute else "DRY-RUN"
    logger.info("Starting image snapshot cleanup (mode=%s)", mode)

    orphans = None
    total_snapshots = 0
    image_snapshot_count = 0

    # In execute mode, try loading cached scan results
    if execute and not no_cache:
        scanned_at, cached_data = load_cache(CACHE_KEY)
        if cached_data is not None:
            console.print(f"[bold green]Loaded cached scan results[/] (scanned at {scanned_at}, {len(cached_data)} orphans)")
            orphans = cached_data
            total_snapshots = len(cached_data)
            image_snapshot_count = len(cached_data)

    # Scan if no cache available
    if orphans is None:
        total_snapshots, image_snapshot_count, orphans = _scan_orphan_snapshots(db_params, wds_client)

    # Report
    report_header = f"\n=== Orphan Image Snapshot Report [{mode}] ==="
    console.print(f"[bold]{report_header}[/]")
    logger.info(report_header)

    report_lines = [
        f"Total WDS snapshots:     {total_snapshots}",
        f"Image volume snapshots:  {image_snapshot_count}",
        f"Orphan (no clones):      {len(orphans)}",
    ]

    for line in report_lines:
        console.print(line)
        logger.info(line)

    if not orphans:
        console.print("No orphan image snapshots found.")
        return {
            "total_snapshots": total_snapshots,
            "image_snapshots": image_snapshot_count,
            "orphan": 0,
            "cleaned": 0,
            "failed": 0,
        }

    _print_orphan_table(orphans)

    # Save cache for later --execute
    save_cache(CACHE_KEY, orphans)
    if not execute:
        console.print(f"\nDry-run mode: {len(orphans)} snapshots would be deleted.")
        console.print("[dim]Scan results cached. Run with --execute to delete using cached data.[/]")

    # Execute deletion
    cleaned = 0
    failed = 0
    if execute:
        cleaned, failed = _delete_orphans(wds_client, orphans, execute=True)
        console.print(f"\nResults: cleaned={cleaned}, failed={failed}")
        logger.info(f"Results: cleaned={cleaned}, failed={failed}")
        # Clear cache after successful execution
        if failed == 0:
            clear_cache(CACHE_KEY)

    return {
        "total_snapshots": total_snapshots,
        "image_snapshots": image_snapshot_count,
        "orphan": len(orphans),
        "cleaned": cleaned,
        "failed": failed,
    }
