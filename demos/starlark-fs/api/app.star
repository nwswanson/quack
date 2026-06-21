def handle(req):
    profile = fs.read("profile.txt").strip()
    manifest = fs.stat("profile.txt")
    assets = fs.listdir(".")
    raw = fs.read_bytes("/raw.bin")

    return (
        200,
        {"content-type": "application/json; charset=utf-8"},
        json.encode_indent({
            "ok": True,
            "message": profile,
            "data_dir": assets,
            "profile": manifest,
            "has_notes": fs.exists("notes.md"),
            "has_missing_file": fs.exists("missing.txt"),
            "raw_size": len(raw),
            "raw_text": str(raw),
        }, indent = "  ") + "\n",
    )
