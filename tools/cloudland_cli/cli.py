"""CloudLand CLI - Toolset for CloudLand IaaS management.

Provides subcommands for various operational tasks:
- clean: Scan and clean zombie WDS resources
"""

import logging
import logging.handlers
import json
import sys
import time

import click

from cloudland_cli.config import load_config
from cloudland_cli.iaas_client import IaaSClient
from cloudland_cli.wds_client import WDSClient
from cloudland_cli.clean_volumes import clean_volumes
from cloudland_cli.clean_images import clean_image_snapshots


def _setup_logging(verbose, log_file):
    """Configure logging level and output.

    Args:
        verbose: If True, set DEBUG level; otherwise WARNING.
        log_file: Path to log file. If provided, logs are appended to this file.
    """
    level = logging.DEBUG if verbose else logging.WARNING

    # Create logger
    logger = logging.getLogger()
    logger.setLevel(level)

    # Format
    formatter = logging.Formatter("%(asctime)s %(levelname)s %(name)s: %(message)s")

    # Remove existing handlers to avoid duplicates
    logger.handlers = []

    # File handler (if log_file specified)
    if log_file:
        file_handler = logging.handlers.RotatingFileHandler(
            log_file, maxBytes=10*1024*1024, backupCount=5
        )
        file_handler.setLevel(level)
        file_handler.setFormatter(formatter)
        logger.addHandler(file_handler)


@click.group()
@click.option("--config", "-c", default="config.toml", help="Path to config.toml")
@click.option("--verbose", "-v", is_flag=True, help="Enable verbose logging")
@click.option("--log-file", default="cloudland_cli.log", help="Log file path (default: cloudland_cli.log)")
@click.option("--wds-address", default=None, help="WDS server address (overrides config)")
@click.option("--wds-user", default=None, help="WDS admin username (overrides config)")
@click.option("--wds-password", default=None, help="WDS admin password (overrides config)")
@click.pass_context
def cli(ctx, config, verbose, log_file, wds_address, wds_user, wds_password):
    """CloudLand CLI - Toolset for CloudLand IaaS management."""
    _setup_logging(verbose, log_file)
    ctx.ensure_object(dict)
    ctx.obj["config_path"] = config
    # Store WDS CLI overrides for subcommands
    ctx.obj["wds_overrides"] = {
        "address": wds_address,
        "admin": wds_user,
        "password": wds_password,
    }


def _load_cfg_and_wds(ctx):
    """Load config and create WDS client from context.

    Args:
        ctx: Click context with config_path and wds_overrides.

    Returns:
        Tuple of (config dict, WDSClient instance).

    Raises:
        SystemExit: If config loading or WDS connection fails.
    """
    try:
        cfg = load_config(ctx.obj["config_path"], ctx.obj["wds_overrides"])
    except Exception as e:
        click.echo(f"Error loading config: {e}", err=True)
        sys.exit(1)

    try:
        wds = WDSClient(cfg["wds"]["address"], cfg["wds"]["admin"], cfg["wds"]["password"])
    except Exception as e:
        # Log connection error without exposing sensitive credentials
        error_msg = str(e)
        # Mask any potential password in error message
        import re
        error_msg = re.sub(r'password[=:][^\s,\]]*', 'password=***MASKED***', error_msg)
        click.echo(f"Error connecting to WDS: {error_msg}", err=True)
        sys.exit(1)

    return cfg, wds


@cli.group()
@click.pass_context
def clean(ctx):
    """Scan and clean zombie resources."""
    pass


@clean.command("volumes")
@click.option("--all", "vol_type", flag_value="all", default=True, help="Clean all volumes (default)")
@click.option("--boot", "vol_type", flag_value="boot", help="Clean boot volumes only")
@click.option("--data", "vol_type", flag_value="data", help="Clean data volumes only")
@click.option("--execute", is_flag=True, help="Actually delete zombie volumes (default: dry-run)")
@click.option("--no-cache", is_flag=True, help="Ignore cached scan results and force re-scan")
@click.pass_context
def clean_volumes_cmd(ctx, vol_type, execute, no_cache):
    """Scan and clean zombie WDS volumes.

    By default runs in dry-run mode, reporting zombies without deleting.
    Add --execute to actually delete zombie volumes from WDS.
    Scan results are cached so --execute can reuse them without re-scanning.
    Use --no-cache to force a fresh scan.
    """
    cfg, wds = _load_cfg_and_wds(ctx)
    result = clean_volumes(cfg["db"], wds, vol_type=vol_type, execute=execute, no_cache=no_cache)

    # Exit with error code if there were failures
    if result["failed"] > 0:
        sys.exit(1)


@clean.command("images")
@click.option("--snapshot", is_flag=True, required=True, help="Clean orphan image snapshots")
@click.option("--execute", is_flag=True, help="Actually delete orphan snapshots (default: dry-run)")
@click.option("--no-cache", is_flag=True, help="Ignore cached scan results and force re-scan")
@click.pass_context
def clean_images_cmd(ctx, snapshot, execute, no_cache):
    """Scan and clean orphan WDS image snapshots.

    Finds image snapshots that have no remaining clone volumes.
    By default runs in dry-run mode. Add --execute to actually delete.
    Scan results are cached so --execute can reuse them without re-scanning.
    Use --no-cache to force a fresh scan.
    """
    cfg, wds = _load_cfg_and_wds(ctx)
    result = clean_image_snapshots(cfg["db"], wds, execute=execute, no_cache=no_cache)

    # Exit with error code if there were failures
    if result["failed"] > 0:
        sys.exit(1)


def _print_output(data, as_json):
    if as_json:
        click.echo(json.dumps(data, ensure_ascii=False, indent=2))
        return
    if data is None:
        click.echo("OK")
    elif isinstance(data, (dict, list)):
        click.echo(json.dumps(data, ensure_ascii=False, indent=2))
    else:
        click.echo(str(data))


@cli.group()
@click.option("--endpoint", required=True, help="CloudLand API endpoint, e.g. https://dev-sv01.raksmart.com")
@click.option("--username", required=True, help="CloudLand username")
@click.option("--password", required=True, help="CloudLand password")
@click.option("--org", default=None, help="Org name (optional)")
@click.option("--insecure", is_flag=True, help="Disable TLS certificate verification")
@click.option("--timeout", default=30, show_default=True, help="HTTP timeout in seconds")
@click.pass_context
def iaas(ctx, endpoint, username, password, org, insecure, timeout):
    """CloudLand IaaS API helper commands (login/list/create/delete)."""
    client = IaaSClient(
        endpoint=endpoint,
        username=username,
        password=password,
        org=org,
        insecure=insecure,
        timeout=timeout,
    )
    try:
        client.login()
    except Exception as e:
        click.echo(f"IaaS login failed: {e}", err=True)
        sys.exit(1)
    ctx.obj["iaas_client"] = client


@iaas.command("zones")
@click.option("--json", "as_json", is_flag=True, help="Print raw JSON")
@click.pass_context
def iaas_zones(ctx, as_json):
    """List zones."""
    data = ctx.obj["iaas_client"].request("GET", "/api/v1/zones")
    _print_output(data, as_json)


@iaas.command("hypers")
@click.option("--limit", default=200, show_default=True, help="Max hypers returned")
@click.option("--json", "as_json", is_flag=True, help="Print raw JSON")
@click.pass_context
def iaas_hypers(ctx, limit, as_json):
    """List hypervisors."""
    data = ctx.obj["iaas_client"].request("GET", "/api/v1/hypers", params={"offset": 0, "limit": limit})
    _print_output(data, as_json)


@iaas.command("images")
@click.option("--limit", default=200, show_default=True, help="Max images returned")
@click.option("--json", "as_json", is_flag=True, help="Print raw JSON")
@click.pass_context
def iaas_images(ctx, limit, as_json):
    """List images."""
    data = ctx.obj["iaas_client"].request("GET", "/api/v1/images", params={"offset": 0, "limit": limit})
    _print_output(data, as_json)


@iaas.command("flavors")
@click.option("--limit", default=200, show_default=True, help="Max flavors returned")
@click.option("--json", "as_json", is_flag=True, help="Print raw JSON")
@click.pass_context
def iaas_flavors(ctx, limit, as_json):
    """List flavors."""
    data = ctx.obj["iaas_client"].request("GET", "/api/v1/flavors", params={"offset": 0, "limit": limit})
    _print_output(data, as_json)


@iaas.command("subnets")
@click.option("--limit", default=200, show_default=True, help="Max subnets returned")
@click.option("--json", "as_json", is_flag=True, help="Print raw JSON")
@click.pass_context
def iaas_subnets(ctx, limit, as_json):
    """List subnets."""
    data = ctx.obj["iaas_client"].request("GET", "/api/v1/subnets", params={"offset": 0, "limit": limit})
    _print_output(data, as_json)


@iaas.group("instances")
def iaas_instances():
    """Instance operations."""
    pass


@iaas_instances.command("list")
@click.option("--limit", default=200, show_default=True, help="Max instances returned")
@click.option("--json", "as_json", is_flag=True, help="Print raw JSON")
@click.pass_context
def iaas_instances_list(ctx, limit, as_json):
    """List instances."""
    data = ctx.obj["iaas_client"].request("GET", "/api/v1/instances", params={"offset": 0, "limit": limit})
    _print_output(data, as_json)


@iaas_instances.command("get")
@click.argument("instance_id")
@click.option("--json", "as_json", is_flag=True, help="Print raw JSON")
@click.pass_context
def iaas_instances_get(ctx, instance_id, as_json):
    """Get one instance by UUID."""
    data = ctx.obj["iaas_client"].request("GET", f"/api/v1/instances/{instance_id}")
    _print_output(data, as_json)


@iaas_instances.command("create")
@click.option("--payload-file", required=True, type=click.Path(exists=True), help="JSON payload file for /api/v1/instances")
@click.option("--json", "as_json", is_flag=True, help="Print raw JSON")
@click.pass_context
def iaas_instances_create(ctx, payload_file, as_json):
    """Create instance(s) using a JSON payload file."""
    with open(payload_file, "r", encoding="utf-8") as f:
        payload = json.load(f)
    data = ctx.obj["iaas_client"].request("POST", "/api/v1/instances", payload=payload)
    _print_output(data, as_json)


@iaas_instances.command("delete")
@click.argument("instance_id")
@click.pass_context
def iaas_instances_delete(ctx, instance_id):
    """Delete one instance by UUID."""
    ctx.obj["iaas_client"].request("DELETE", f"/api/v1/instances/{instance_id}")
    click.echo(f"Deleted instance {instance_id}")


@iaas_instances.command("wait")
@click.argument("instance_id")
@click.option("--status", "expected_status", required=True, help="Expected status, e.g. running|active|error")
@click.option("--timeout", "timeout_sec", default=900, show_default=True, help="Wait timeout seconds")
@click.option("--interval", default=5, show_default=True, help="Poll interval seconds")
@click.pass_context
def iaas_instances_wait(ctx, instance_id, expected_status, timeout_sec, interval):
    """Wait until instance reaches expected status."""
    deadline = time.time() + timeout_sec
    expected = expected_status.lower()
    while time.time() < deadline:
        data = ctx.obj["iaas_client"].request("GET", f"/api/v1/instances/{instance_id}")
        current = str(data.get("status", "")).lower()
        click.echo(f"instance={instance_id} status={current}")
        if current == expected:
            click.echo("Reached expected status")
            return
        if current == "error":
            click.echo("Instance entered error status", err=True)
            sys.exit(1)
        time.sleep(interval)
    click.echo(f"Timeout waiting for {instance_id} to reach {expected_status}", err=True)
    sys.exit(1)
