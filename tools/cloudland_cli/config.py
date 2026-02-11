"""Configuration parser for CloudLand CLI.

Reads WDS credentials from tools/config.toml and database connection
info from the CloudLand config file referenced within it.
CLI options can override WDS settings from the config file.
"""

import os
import re

import tomli


def _parse_gorm_uri(uri):
    """Parse a GORM-style PostgreSQL URI into connection parameters.

    Args:
        uri: GORM connection string, e.g.
             "host=127.0.0.1 port=5432 user=postgres password=xxx dbname=cloudland sslmode=disable"

    Returns:
        dict with keys: host, port, user, password, dbname, sslmode.
    """
    params = {}
    for match in re.finditer(r"(\w+)=(\S+)", uri):
        params[match.group(1)] = match.group(2)
    return params


def load_config(config_path="config.toml", wds_overrides=None):
    """Load and merge WDS config and CloudLand DB config.

    Args:
        config_path: Path to tools/config.toml.
        wds_overrides: Optional dict with 'address', 'admin', 'password'
                       from CLI options. Non-None values override config file.

    Returns:
        dict with 'wds' and 'db' sections.
    """
    with open(config_path, "rb") as f:
        cfg = tomli.load(f)

    # WDS config: start from file, then apply CLI overrides
    wds = cfg.get("wds", {})
    wds_cfg = {
        "address": wds.get("address", ""),
        "admin": wds.get("admin", ""),
        "password": wds.get("password", ""),
    }
    if wds_overrides:
        for key in ("address", "admin", "password"):
            if wds_overrides.get(key):
                wds_cfg[key] = wds_overrides[key]
    wds_cfg["address"] = wds_cfg["address"].rstrip("/")

    # DB config: read from CloudLand config file
    cloudland_config_path = cfg["cloudland"]["config_path"]
    if not os.path.isabs(cloudland_config_path):
        base_dir = os.path.dirname(os.path.abspath(config_path))
        cloudland_config_path = os.path.join(base_dir, cloudland_config_path)

    with open(cloudland_config_path, "rb") as f:
        cl_cfg = tomli.load(f)

    db_params = _parse_gorm_uri(cl_cfg["db"]["uri"])

    return {
        "wds": wds_cfg,
        "db": db_params,
    }
