def handle(req):
    frame = camera.capture("video0", width = 640, height = 480, format = "MJPG")
    return (
        200,
        {
            "content-type": frame["mime_type"],
            "cache-control": "no-store",
            "x-camera-alias": "video0",
        },
        frame["data"],
    )
