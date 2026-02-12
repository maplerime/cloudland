"""WDS API client for CloudLand Cleaner.

Handles authentication, volume operations, and snapshot operations
against the WDS distributed storage service.
"""

import logging
import urllib3

import requests

# Suppress InsecureRequestWarning for self-signed certs (matches shell scripts using curl -k)
urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

logger = logging.getLogger(__name__)


def _mask_sensitive_data(data):
    """Mask sensitive information in logs.

    Args:
        data: Dictionary or string containing potentially sensitive data.

    Returns:
        Masked version of the data safe for logging.
    """
    # Keys that may contain secrets and should be masked when found.
    sensitive_keys = {
        "password",
        "pass",
        "secret",
        "token",
        "access_token",
        "refresh_token",
        "api_key",
        "key",
        "authorization",
        "auth",
    }

    def _mask(obj):
        # Recursively walk dicts/lists and mask any sensitive-looking keys.
        if isinstance(obj, dict):
            masked_dict = {}
            for k, v in obj.items():
                if isinstance(k, str) and k.lower() in sensitive_keys:
                    masked_dict[k] = "***MASKED***"
                else:
                    masked_dict[k] = _mask(v)
            return masked_dict
        if isinstance(obj, list):
            return [_mask(item) for item in obj]
        # Leave scalars as-is
        return obj

    if isinstance(data, (dict, list)):
        return _mask(data)
    elif isinstance(data, str):
        # Mask common password and token patterns in text representations.
        import re
        masked = data
        # JSON-style "password": "value" or 'password': 'value'
        masked = re.sub(
            r'(["\'])password\1\s*:\s*(["\'])(?:(?!\2).)*\2',
            r'"password": "***MASKED***"',
            masked,
            flags=re.IGNORECASE,
        )
        # password=value (e.g. URL or form-encoded)
        masked = re.sub(
            r'password=[^&\s"\']*',
            'password=***MASKED***',
            masked,
            flags=re.IGNORECASE,
        )
        # Generic token-like fields
        masked = re.sub(
            r'(?:access_token|refresh_token|api_key|token)=([^&\s"\']*)',
            lambda m: m.group(0).split("=", 1)[0] + "=***MASKED***",
            masked,
            flags=re.IGNORECASE,
        )
        return masked
    return data


class WDSClient:
    """Client for WDS distributed storage API.

    Uses requests.Session to reuse HTTP connections for better performance.

    Attributes:
        address: WDS server base URL.
        token: Bearer token obtained after login.
        session: requests.Session for connection pooling.
    """

    def __init__(self, address, admin, password):
        """Initialize WDS client and authenticate.

        Args:
            address: WDS server base URL, e.g. "https://wds-server:port".
            admin: Admin username.
            password: Admin password.
        """
        self.address = address.rstrip("/")
        self.token = None
        # Create session for connection pooling
        self.session = requests.Session()
        self.session.verify = False  # Disable SSL verification globally for session
        self._login(admin, password)

    def __del__(self):
        """Clean up session when client is destroyed."""
        if hasattr(self, 'session'):
            self.session.close()

    def close(self):
        """Explicitly close the session."""
        if self.session:
            self.session.close()

    def _request_with_logging(self, method, url, **kwargs):
        """Execute HTTP request with error logging.

        Args:
            method: HTTP method ('GET', 'POST', 'PUT', 'DELETE').
            url: Full URL to request.
            **kwargs: Additional arguments to pass to session method.

        Returns:
            requests.Response object.
        """
        try:
            # Prepare request details for logging
            req_json = kwargs.get('json')
            req_params = kwargs.get('params')

            # Execute request
            if method.upper() == 'GET':
                resp = self.session.get(url, **kwargs)
            elif method.upper() == 'POST':
                resp = self.session.post(url, **kwargs)
            elif method.upper() == 'PUT':
                resp = self.session.put(url, **kwargs)
            elif method.upper() == 'DELETE':
                resp = self.session.delete(url, **kwargs)
            else:
                raise ValueError(f"Unsupported HTTP method: {method}")

            # Log errors based on status code
            if resp.status_code >= 400:
                log_msg = f"WDS API error: {method} {url}"
                if req_params:
                    log_msg += f"\n  Params: {_mask_sensitive_data(req_params)}"
                if req_json:
                    log_msg += f"\n  Request body: {_mask_sensitive_data(req_json)}"
                log_msg += f"\n  Response status: {resp.status_code}"
                try:
                    resp_body = resp.json()
                except Exception:
                    resp_body = resp.text
                log_msg += f"\n  Response body: {_mask_sensitive_data(resp_body)}"
                logger.error(log_msg)
            # Avoid logging full request bodies or params, which may contain sensitive data.

            return resp
        except Exception as e:
            logger.error(
                f"WDS API request failed: {method} {url}\n"
                f"  Request body: ***REDACTED***\n"
                f"  Request params: ***REDACTED***\n"
                f"  Error: {e}",
                exc_info=True
            )
            raise

    def _login(self, admin, password):
        """Authenticate with WDS and store the bearer token.

        Args:
            admin: Admin username.
            password: Admin password.

        Raises:
            RuntimeError: If login fails.
        """
        resp = self._request_with_logging(
            'POST',
            f"{self.address}/api/v1/login",
            json={"name": admin, "password": password},
        )
        resp.raise_for_status()
        data = resp.json()
        access_token = data.get("access_token")
        if not access_token:
            raise RuntimeError(f"WDS login failed: {_mask_sensitive_data(data)}")
        self.token = access_token

    def _headers(self):
        """Return authorization headers.

        Returns:
            dict with Authorization and Content-Type headers.
        """
        return {
            "Authorization": f"bearer {self.token}",
            "Content-Type": "application/json",
        }

    def volume_exists(self, volume_id):
        """Check if a WDS volume exists.

        Args:
            volume_id: WDS volume ID.

        Returns:
            True if the volume exists, False otherwise.
        """
        resp = self._request_with_logging(
            'GET',
            f"{self.address}/api/v2/sync/block/volumes/{volume_id}",
            headers=self._headers(),
        )
        return resp.status_code == 200

    def get_volume_detail(self, volume_id):
        """Get detailed information about a WDS volume.

        Args:
            volume_id: WDS volume ID.

        Returns:
            dict with volume details (including data_size), or None on error.
        """
        resp = self._request_with_logging(
            'GET',
            f"{self.address}/api/v2/sync/block/volumes/{volume_id}",
            headers=self._headers(),
        )
        if resp.status_code != 200:
            return None
        try:
            data = resp.json()
            # WDS API returns volume_detail nested in response
            volume_detail = data.get("volume_detail")
            if volume_detail:
                return volume_detail
            # Fall back to volume field if available
            return data.get("volume", data) if data.get("volume") else None
        except Exception:
            return None

    def get_volume_vhosts(self, volume_id):
        """Get vhosts bound to a volume.

        Args:
            volume_id: WDS volume ID.

        Returns:
            List of vhost dicts from WDS API, or empty list on error.
        """
        resp = self._request_with_logging(
            'GET',
            f"{self.address}/api/v2/block/volumes/{volume_id}/vhost",
            headers=self._headers(),
        )
        if resp.status_code != 200:
            return []
        data = resp.json()
        return data.get("vhosts", [])

    def get_vhost_bound_uss(self, vhost_id):
        """Get USS (storage servers) bound to a vhost.

        Args:
            vhost_id: WDS vhost ID.

        Returns:
            List of USS dicts from WDS API, or empty list on error.
        """
        resp = self._request_with_logging(
            'GET',
            f"{self.address}/api/v2/sync/block/vhost/{vhost_id}/vhost_binded_uss",
            headers=self._headers(),
        )
        if resp.status_code != 200:
            return []
        data = resp.json()
        return data.get("uss", [])

    def unbind_uss(self, vhost_id, uss_id):
        """Unbind a USS from a vhost.

        Args:
            vhost_id: WDS vhost ID.
            uss_id: WDS USS (storage server) ID.

        Returns:
            Tuple of (success: bool, response_text: str).
        """
        resp = self._request_with_logging(
            'PUT',
            f"{self.address}/api/v2/sync/block/vhost/unbind_uss",
            json={"vhost_id": vhost_id, "uss_gw_id": uss_id},
            headers=self._headers(),
        )
        try:
            data = resp.json()
            # Check WDS API response: ret_code='0' or result=True indicates success
            success = (data.get("ret_code") == "0" or data.get("result") is True)
            return success, data
        except Exception:
            # Fall back to HTTP status code if JSON parsing fails
            success = resp.status_code == 200
            return success, resp.text

    def delete_vhost(self, vhost_id):
        """Delete a WDS vhost.

        Args:
            vhost_id: WDS vhost ID.

        Returns:
            Tuple of (success: bool, response_text: str).
        """
        resp = self._request_with_logging(
            'DELETE',
            f"{self.address}/api/v2/sync/block/vhost/{vhost_id}",
            headers=self._headers(),
        )
        try:
            data = resp.json()
            # Check WDS API response: ret_code='0' or result=True indicates success
            success = (data.get("ret_code") == "0" or data.get("result") is True)
            return success, data
        except Exception:
            # Fall back to HTTP status code if JSON parsing fails
            success = resp.status_code == 200
            return success, resp.text

    def delete_volume(self, volume_id):
        """Force-delete a WDS volume.

        Args:
            volume_id: WDS volume ID.

        Returns:
            Tuple of (success: bool, response_text: str).
            success is True if ret_code is '0' or result is True.
            response_text is the response body or error message.
        """
        resp = self._request_with_logging(
            'DELETE',
            f"{self.address}/api/v2/sync/block/volumes/{volume_id}",
            params={"force": "true"},
            headers=self._headers(),
        )
        try:
            data = resp.json()
            # Check WDS API response: ret_code='0' or result=True indicates success
            success = (data.get("ret_code") == "0" or data.get("result") is True)
            return success, data
        except Exception:
            # Fall back to HTTP status code if JSON parsing fails
            success = resp.status_code == 200
            return success, resp.text

    def list_snapshots(self):
        """List all WDS snapshots.

        Returns:
            List of snapshot dicts from WDS API.
        """
        resp = self._request_with_logging(
            'GET',
            f"{self.address}/api/v2/block/snaps",
            params={"index": 0, "offset": 10000},
            headers=self._headers(),
        )
        resp.raise_for_status()
        data = resp.json()
        # API returns snapshots in a 'snaps' field
        if isinstance(data, dict):
            return data.get("snaps", data.get("data", []))
        return data

    def get_clone_volumes(self, snapshot_id):
        """Get volumes cloned from a snapshot.

        Args:
            snapshot_id: WDS snapshot ID.

        Returns:
            List of volume dicts cloned from this snapshot.
        """
        resp = self._request_with_logging(
            'GET',
            f"{self.address}/api/v2/block/volumes",
            params={"parent_snap_id": snapshot_id},
            headers=self._headers(),
        )
        resp.raise_for_status()
        data = resp.json()
        if isinstance(data, dict):
            return data.get("volumes", data.get("data", []))
        return data

    def delete_snapshot(self, snapshot_id):
        """Force-delete a WDS snapshot.

        Args:
            snapshot_id: WDS snapshot ID.

        Returns:
            Tuple of (success: bool, response_text: str).
            success is True if ret_code is '0' or result is True.
            response_text is the response body or error message.
        """
        resp = self._request_with_logging(
            'DELETE',
            f"{self.address}/api/v2/sync/block/snaps/{snapshot_id}",
            params={"force": "true"},
            headers=self._headers(),
        )
        try:
            data = resp.json()
            # Check WDS API response: ret_code='0' or result=True indicates success
            success = (data.get("ret_code") == "0" or data.get("result") is True)
            return success, data
        except Exception:
            # Fall back to HTTP status code if JSON parsing fails
            success = resp.status_code == 200
            return success, resp.text
