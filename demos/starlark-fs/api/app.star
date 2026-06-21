def handle(req):
    profile = fs.read("data/profile.txt").strip()
    manifest = fs.stat("data/profile.txt")
    assets = fs.listdir("data")
    raw = fs.read_bytes("/data/raw.bin")

    return (
        200,
        {"content-type": "application/json; charset=utf-8"},
        json.encode_indent({
            "ok": True,
            "message": profile,
            "data_dir": assets,
            "profile": manifest,
            "has_notes": fs.exists("data/notes.md"),
            "has_missing_file": fs.exists("data/missing.txt"),
            "raw_size": len(raw),
            "raw_text": str(raw),
        }, indent = "  ") + "\n",
    )
