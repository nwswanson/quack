def _escape_html(value):
    return str(value).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace("\"", "&quot;")

def handle(req):
    resp = http.get("api://ifconfig/", headers = {
        "user-agent": "curl/8.0 quack-ifconfig-demo",
    }, options = {
        "timeout": "1s",
        "follow_redirects": False,
    })
    body = resp.text.strip()
    html = """<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ifconfig.me</title>
  <style>
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      font: 16px/1.5 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: #17202a;
      background: #f5f7fa;
    }
    main {
      width: min(720px, calc(100vw - 32px));
    }
    h1 {
      margin: 0 0 12px;
      font-size: 32px;
      font-weight: 700;
    }
    pre {
      margin: 16px 0 0;
      padding: 16px;
      overflow: auto;
      border: 1px solid #d7dde5;
      border-radius: 8px;
      background: #ffffff;
      font-size: 15px;
    }
  </style>
</head>
<body>
  <main>
    <h1>https://ifconfig.me/</h1>
    <p>Status: %s</p>
    <pre>%s</pre>
  </main>
</body>
</html>
""" % (resp.status_code, _escape_html(body))
    return (200, {"content-type": "text/html; charset=utf-8"}, html)
