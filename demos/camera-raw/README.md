# Camera Raw Demo

This demo exposes two raw camera capture endpoints:

- `/video0` captures logical camera alias `video0`
- `/video1` captures logical camera alias `video1`

In the admin UI, create two hardware devices:

```yaml
devices:
  - id: video0
    kind: uvc-camera
    path: /dev/video0
    label: Video 0
    site: camera-raw
  - id: video1
    kind: uvc-camera
    path: /dev/video1
    label: Video 1
    site: camera-raw
```

The container still needs `/dev/video0` and `/dev/video1` mounted into the pod, and the server image must run with the hardware plugin enabled.
