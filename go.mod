module github.com/b0ch3nski/go-starlink/model

go 1.21

require (
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/grandcat/zeroconf v1.0.0
	github.com/prometheus/client_golang v1.18.0
	google.golang.org/grpc v1.62.2
	google.golang.org/protobuf v1.34.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/matttproud/golang_protobuf_extensions/v2 v2.0.0 // indirect
	github.com/miekg/dns v1.1.27 // indirect
	github.com/prometheus/client_model v0.5.0 // indirect
	github.com/prometheus/common v0.45.0 // indirect
	github.com/prometheus/procfs v0.12.0 // indirect
	golang.org/x/crypto v0.18.0 // indirect
	golang.org/x/net v0.20.0 // indirect
	golang.org/x/sys v0.16.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240123012728-ef4313101c80 // indirect
)

replace github.com/b0ch3nski/go-starlink/model/device => ./api-protoc/device

replace github.com/b0ch3nski/go-starlink/model/device/services/unlock => ./api-protoc/device/services/unlock

replace github.com/b0ch3nski/go-starlink/model/status => ./api-protoc/status

replace github.com/b0ch3nski/go-starlink/model/telemetron => ./api-protoc/telemetron

replace github.com/b0ch3nski/go-starlink/model/satellites => ./api-protoc/satellites
