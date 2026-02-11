"""CloudLand CLI - Toolset for CloudLand IaaS management.

Provides subcommands for various operational tasks:
- clean: Scan and clean zombie WDS resources
"""

import logging
import logging.handlers
import sys

import click

from cloudland_cli.config import load_config
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
