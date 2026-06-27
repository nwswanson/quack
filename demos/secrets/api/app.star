def _escape_html(value):
    return str(value).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace("\"", "&quot;")

def _page(title, body):
    return """<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <style>
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      font: 16px/1.5 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: #1f2933;
      background: #f4f7fb;
    }
    main {
      width: min(720px, calc(100vw - 32px));
    }
    h1 {
      margin: 0 0 12px;
      font-size: 32px;
      letter-spacing: 0;
    }
    .secret {
      margin-top: 18px;
      padding: 18px 20px;
      border: 1px solid #cbd6e2;
      border-radius: 8px;
      background: #ffffff;
      font-size: 22px;
      font-weight: 700;
      overflow-wrap: anywhere;
    }
    .secret + .secret {
      margin-top: 12px;
    }
    .missing {
      color: #6b7280;
      font-weight: 500;
    }
    code {
      font: 14px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }
  </style>
</head>
<body>
  <main>
    %s
  </main>
</body>
</html>
""" % (_escape_html(title), body)

def handle(req):
    if not secret.unlocked():
        body = """
    <h1>Secrets demo</h1>
    <p>The secret store is locked. Unlock it in the admin UI, then set <code>hello</code> for this site.</p>
"""
        return (423, {"content-type": "text/html; charset=utf-8"}, _page("Secrets locked", body))

    hello_body = '<span class="missing">missing</span>'
    if secret.exists("site", "hello"):
        hello_body = _escape_html(secret.get("site", "hello"))

    hello2_body = '<span class="missing">missing</span>'
    if secret.exists("site", "hello2"):
        hello2_body = _escape_html(secret.get("site", "hello2"))

    body = """
    <h1>Secrets demo</h1>
    <p>Secrets are encrypted with an auto-generated base key, which is itself encrypted with an unlock key (that can be changed), so they are encrypted at rest via AES/GCM. It hasn't been pentested etc, but it is at least better than storing in memory/on-disk. On server boot the admin needs to enter the passcode to unlock the server store--this lasts until server reboot. </p>
    <p>Each value is guarded with <code>secret.exists("site", name)</code> before calling <code>secret.get</code>.</p>
    <h3>Example</h3>
    <code>
    hello_body = '<span class="missing">missing</span>'<br />
    if secret.exists("site", "hello"):<br />
        &nbsp;&nbsp;hello_body = _escape_html(secret.get("site", "hello"))<br />
    </code>
    <div class="secret"><code>hello</code>: %s</div>
    <div class="secret"><code>hello2</code>: %s</div>
""" % (hello_body, hello2_body)
    return (200, {"content-type": "text/html; charset=utf-8"}, _page("Secrets demo", body))
