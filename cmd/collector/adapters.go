package main

import (
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/ntcip"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// RegisterAdapters wires every compiled-in adapter into the registry. This is
// the one place the binary decides which vendors it ships with; contributing an
// adapter means adding a line here plus internal/vendors/<vendor>/<kind>.go.
func RegisterAdapters(r *adapter.Registry) {
	ntcip.RegisterTo(r)
	cameleon.RegisterTo(r)
}