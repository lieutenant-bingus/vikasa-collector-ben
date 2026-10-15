module github.com/Vikasa2M/vikasa-collector

go 1.26.0

toolchain go1.26.9

require (
	github.com/Vikasa2M/openits-models v0.5.0
	github.com/gosnmp/gosnmp v1.45.0
	github.com/nats-io/jwt/v2 v2.8.2
	github.com/nats-io/nats-server/v2 v2.15.0
	github.com/nats-io/nats.go v1.54.0
	github.com/nats-io/nkeys v0.4.16
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

// plc co-development: gate events live on the models fork until tagged on the
// canonical module. Remote replace (CI-fetchable); local ../ paths are still
// rejected by scripts/lint-boundary.sh (ADR 0010).
replace github.com/Vikasa2M/openits-models => github.com/lieutenant-bingus/openits-models-ben v0.0.0-20260915134907-35e25b85e1d6

require (
	github.com/antithesishq/antithesis-sdk-go v0.8.0-default-no-op // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/time v0.16.0 // indirect
)
