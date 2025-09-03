module github.com/b0ch3nski/go-starlink/model

go 1.21

require (
	google.golang.org/grpc v1.62.2
	google.golang.org/protobuf v1.34.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/golang/protobuf v1.5.3 // indirect
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
