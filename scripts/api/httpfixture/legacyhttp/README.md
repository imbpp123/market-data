# Historical HTTP benchmark transport

Frozen from `internal/transport/http` at revision `201289fa7ca2e240cbbf8f6d0df2e4e1fb854482` (phase 2).
Only the HTTP benchmark fixture imports this package. It preserves the recorded
HTTP response bytes for comparison with gRPC. The service and its Docker image
do not import or include this package. Do not use it as a supported API.
