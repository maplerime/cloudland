"""PostgreSQL database operations for CloudLand Cleaner.

Queries soft-deleted volumes and image storage records
from the CloudLand database.
"""

import psycopg2


def _connect(db_params):
    """Create a PostgreSQL connection from parsed GORM URI params.

    Args:
        db_params: dict with host, port, user, password, dbname keys.

    Returns:
        psycopg2 connection object.
    """
    return psycopg2.connect(
        host=db_params.get("host", "127.0.0.1"),
        port=int(db_params.get("port", 5432)),
        user=db_params.get("user", "postgres"),
        password=db_params.get("password", ""),
        dbname=db_params.get("dbname", "cloudland"),
    )


def count_deleted_volumes(db_params, vol_type="all"):
    """Count total soft-deleted WDS volumes in the database.

    Args:
        db_params: Database connection parameters.
        vol_type: 'all', 'boot', or 'data' to filter by booting flag.

    Returns:
        Total count of matching records.
    """
    sql = """
        SELECT COUNT(*)
        FROM volumes
        WHERE deleted_at IS NOT NULL
          AND path LIKE 'wds_%%'
    """
    if vol_type == "boot":
        sql += "  AND booting = true\n"
    elif vol_type == "data":
        sql += "  AND booting = false\n"

    conn = _connect(db_params)
    try:
        with conn.cursor() as cur:
            cur.execute(sql)
            return cur.fetchone()[0]
    finally:
        conn.close()


def get_deleted_volumes(db_params, vol_type="all", start_after_id=0):
    """Query soft-deleted WDS volumes from the database.

    Results are ordered by id ASC to support incremental checkpoint scanning.

    Args:
        db_params: Database connection parameters.
        vol_type: 'all', 'boot', or 'data' to filter by booting flag.
        start_after_id: Only return volumes with id > this value (for resume).

    Returns:
        List of dicts with id, uuid, name, path, booting, status, deleted_at.
    """
    sql = """
        SELECT id, uuid, name, path, booting, status, deleted_at
        FROM volumes
        WHERE deleted_at IS NOT NULL
          AND path LIKE 'wds_%%'
          AND id > %s
    """
    # Filter by volume type
    if vol_type == "boot":
        sql += "  AND booting = true\n"
    elif vol_type == "data":
        sql += "  AND booting = false\n"

    sql += "ORDER BY id ASC"

    conn = _connect(db_params)
    try:
        with conn.cursor() as cur:
            cur.execute(sql, (start_after_id,))
            columns = [desc[0] for desc in cur.description]
            return [dict(zip(columns, row)) for row in cur.fetchall()]
    finally:
        conn.close()


def get_image_volume_ids(db_params):
    """Get all WDS volume IDs from image_storages table.

    Args:
        db_params: Database connection parameters.

    Returns:
        Set of WDS volume ID strings.
    """
    sql = """
        SELECT volume_id
        FROM image_storages
        WHERE volume_id IS NOT NULL
          AND volume_id != ''
    """
    conn = _connect(db_params)
    try:
        with conn.cursor() as cur:
            cur.execute(sql)
            return {row[0] for row in cur.fetchall()}
    finally:
        conn.close()
