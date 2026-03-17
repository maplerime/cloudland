"""Scan result cache and checkpoint for CloudLand Cleaner.

Supports two modes:
- Simple cache: save/load a list of results (used by clean_images)
- Checkpoint cache: save/load incremental scan progress with last_checked_id
  and accumulated zombies (used by clean_volumes)
"""

import json
import os
from datetime import datetime, timezone


CACHE_DIR = ".cache"


def _cache_path(name):
    """Build the full path for a cache file.

    Args:
        name: Cache key name, e.g. 'clean_volumes_boot'.

    Returns:
        Absolute path string.
    """
    return os.path.join(CACHE_DIR, f"{name}.json")


def _json_serial(obj):
    """JSON serializer for objects not serializable by default json code.

    Handles datetime objects by converting to ISO format string.

    Args:
        obj: Object to serialize.

    Returns:
        ISO format string for datetime objects.

    Raises:
        TypeError: If the object type is not serializable.
    """
    if isinstance(obj, datetime):
        return obj.isoformat()
    raise TypeError(f"Type {type(obj)} not serializable")


# --- Simple cache (for clean_images) ---

def save_cache(name, data):
    """Save scan results to a cache file.

    Args:
        name: Cache key name.
        data: List of dicts to cache (e.g. orphan snapshots).
    """
    os.makedirs(CACHE_DIR, exist_ok=True)
    payload = {
        "scanned_at": datetime.now(timezone.utc).isoformat(),
        "count": len(data),
        "data": data,
    }
    with open(_cache_path(name), "w") as f:
        json.dump(payload, f, indent=2, default=_json_serial)


def load_cache(name):
    """Load scan results from a cache file.

    Args:
        name: Cache key name.

    Returns:
        Tuple of (scanned_at string, data list), or (None, None) if
        no cache exists.
    """
    path = _cache_path(name)
    if not os.path.exists(path):
        return None, None
    with open(path) as f:
        payload = json.load(f)
    return payload["scanned_at"], payload["data"]


def clear_cache(name):
    """Remove a cache file after successful execution.

    Args:
        name: Cache key name.
    """
    path = _cache_path(name)
    if os.path.exists(path):
        os.remove(path)


# --- Checkpoint cache (for clean_volumes) ---

def save_checkpoint(name, last_checked_id, zombies, total_db, checked_count):
    """Save incremental scan checkpoint.

    Called after each volume is checked so progress can be resumed.

    Args:
        name: Cache key name.
        last_checked_id: The DB id of the last volume that was checked.
        zombies: Accumulated list of zombie volume dicts found so far.
        total_db: Total soft-deleted volume count in DB.
        checked_count: Number of volumes checked so far (across all runs).
    """
    os.makedirs(CACHE_DIR, exist_ok=True)
    payload = {
        "updated_at": datetime.now(timezone.utc).isoformat(),
        "last_checked_id": last_checked_id,
        "total_db": total_db,
        "checked_count": checked_count,
        "zombie_count": len(zombies),
        "zombies": zombies,
    }
    with open(_cache_path(name), "w") as f:
        json.dump(payload, f, indent=2, default=_json_serial)


def load_checkpoint(name):
    """Load incremental scan checkpoint.

    Args:
        name: Cache key name.

    Returns:
        dict with last_checked_id, zombies, total_db, checked_count,
        updated_at; or None if no checkpoint exists.
    """
    path = _cache_path(name)
    if not os.path.exists(path):
        return None
    with open(path) as f:
        return json.load(f)
