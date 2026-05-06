"""CloudLand IaaS API client used by cloudland_cli iaas commands."""

from __future__ import annotations

import json
from typing import Any, Dict, Optional

import requests


class IaaSClient:
    """Small wrapper around CloudLand REST API v1."""

    def __init__(self, endpoint: str, username: str, password: str, org: Optional[str] = None,
                 insecure: bool = False, timeout: int = 30) -> None:
        self.endpoint = endpoint.rstrip("/")
        self.username = username
        self.password = password
        self.org = org
        self.verify = not insecure
        self.timeout = timeout
        self.token: Optional[str] = None

    def login(self) -> str:
        payload: Dict[str, Any] = {
            "username": self.username,
            "password": self.password,
        }
        if self.org:
            payload["org"] = {"name": self.org}

        resp = requests.post(
            f"{self.endpoint}/api/v1/login",
            json=payload,
            timeout=self.timeout,
            verify=self.verify,
        )
        resp.raise_for_status()
        data = resp.json()
        token = data.get("access_token") or data.get("token")
        if not token:
            raise RuntimeError(f"login response missing access_token: {json.dumps(data, ensure_ascii=False)}")
        self.token = token
        return token

    def _headers(self) -> Dict[str, str]:
        if not self.token:
            raise RuntimeError("not logged in")
        return {
            "Authorization": f"bearer {self.token}",
            "Content-Type": "application/json",
        }

    def request(self, method: str, path: str, payload: Optional[Dict[str, Any]] = None,
                params: Optional[Dict[str, Any]] = None) -> Any:
        if not path.startswith("/"):
            path = "/" + path
        resp = requests.request(
            method=method.upper(),
            url=f"{self.endpoint}{path}",
            headers=self._headers(),
            json=payload,
            params=params,
            timeout=self.timeout,
            verify=self.verify,
        )
        if resp.status_code == 204:
            return None
        if resp.status_code >= 400:
            body = resp.text.strip()
            raise RuntimeError(f"HTTP {resp.status_code} {method.upper()} {path}: {body}")
        if not resp.text:
            return None
        return resp.json()
