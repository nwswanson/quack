# Site Exclusions Demo

This demo shows `site.yml` upload exclusions. Deploy it with:

```bash
go run ./cmd/quack deploy demos/site-exclusions site-exclusions \
  --token dev-token \
  --serverURL http://localhost:8080
```

The CLI reads `site.yml` before streaming the archive. The upload should include `index.html` and `site.yml`, while these local-only files are skipped:

- `README.md`
- `notes.local`
- `scratch/generated.txt`
- `vendor-cache/package/index.js`
