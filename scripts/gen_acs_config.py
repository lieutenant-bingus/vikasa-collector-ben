"""Generate a gitignored collector-acs.local.yaml from collector-handoff.

Real cabinet addresses stay out of git (see *.local.yaml in .gitignore).
Usage (from vikasa-collector-ben):

  python scripts/gen_acs_config.py

Then:

  ACS_CONFIG=configs/collector-acs.local.yaml go test ./internal/vendors/cameleon -run TestLiveAllACS -v
"""
from __future__ import annotations

import re
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
HANDOFF = REPO.parent / "collector-handoff" / "config"
OUT = REPO / "configs" / "collector-acs.local.yaml"


def parse_handoff(text: str):
    cab = re.search(r"cabinet:\s*'([^']+)'", text).group(1)
    host = re.search(r"host:\s*'([^']+)'", text).group(1)
    port = int(re.search(r"port:\s*(\d+)", text).group(1))
    start = int(re.search(r"dsw:\s*\n\s*start:\s*(\d+)", text).group(1))
    length = int(re.search(r"length:\s*(\d+)", text).group(1))
    devices = []
    for b in re.split(r"\n  - name:", text)[1:]:
        name = re.match(r"\s*'([^']+)'", b).group(1)
        typ = re.search(r"type:\s*(\S+)", b).group(1)
        kind_m = re.search(r"kind:\s*(\S+)", b)
        kind = kind_m.group(1) if kind_m else "unknown"
        dsw = int(re.search(r"dsw:\s*(\d+)", b).group(1))
        devices.append({"name": name, "type": typ, "kind": kind, "dsw": dsw})
    return cab, host, port, start, length, devices


def short_id(name: str) -> str:
    m = re.search(r"GDOT-((?:WG|BG)-\d+)", name)
    if m:
        return m.group(1)
    m = re.search(r"((?:WG|BG)-\d+)", name)
    if m:
        return m.group(1)
    return re.sub(r"[^A-Za-z0-9]+", "-", name).strip("-")[:40]


def main() -> None:
    if not HANDOFF.is_dir():
        raise SystemExit(f"handoff config dir not found: {HANDOFF}")

    cabs = []
    for p in sorted(HANDOFF.glob("acs*.yaml")):
        cab, host, port, start, length, devices = parse_handoff(p.read_text(encoding="utf-8"))
        gates = [d for d in devices if d["type"] == "gate"]
        cabinet = {}
        for d in devices:
            n = d["name"].lower()
            if "lf_fault" in n:
                cabinet["lf_fault"] = d["dsw"]
            elif "sp_fault" in n:
                cabinet["sp_fault"] = d["dsw"]
            elif "door switch" in n:
                cabinet["door"] = d["dsw"]
            elif "gate e-stop" in n:
                cabinet["gate_estop"] = d["dsw"]
        cabs.append(
            {
                "id": p.stem,
                "host": host,
                "port": port,
                "start": start,
                "length": length,
                "gates": gates,
                "cabinet": cabinet,
            }
        )

    lines = [
        "# LOCAL ONLY — gitignored (*.local.yaml). Do not commit.",
        "# Generated from collector-handoff/config by scripts/gen_acs_config.py.",
        "# Scope: gate status + gate/cabinet faults. CMS/signs are out of band.",
        "",
        "region: us-ga",
        "agency: gdot",
        "agency_unit: rlcs",
        "site: reversible-lanes",
        "collector_id: rlcs-acs-collector",
        "model_version: openits/v1",
        "",
        "devices:",
    ]
    for c in cabs:
        lines += [
            f"  - id: {c['id']}",
            "    vendor: cameleon",
            "    device_kind: acs",
            "    poll_interval: 2s",
            "    connection:",
            "      srtp:",
            f'        address: "{c["host"]}:{c["port"]}"',
            '        timeout: "5s"',
            "      dsw:",
            f"        start: {c['start']}",
            f"        length: {c['length']}",
            "      gates:",
        ]
        seen = set()
        for g in c["gates"]:
            gid = short_id(g["name"])
            if gid in seen:
                gid = f"{gid}-{g['dsw']}"
            seen.add(gid)
            kind = g["kind"] if g["kind"] in ("warning", "barrier", "gate", "unknown") else "unknown"
            lines.append(f'        - {{ id: "{gid}", dsw: {g["dsw"]}, kind: "{kind}" }}')
        if c["cabinet"]:
            lines.append("      cabinet:")
            for k in ("lf_fault", "sp_fault", "door", "gate_estop"):
                if k in c["cabinet"]:
                    lines.append(f"        {k}: {c['cabinet'][k]}")
        lines.append("")

    OUT.parent.mkdir(exist_ok=True)
    OUT.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"wrote {OUT} ({len(cabs)} devices) — gitignored; not for commit")


if __name__ == "__main__":
    main()
