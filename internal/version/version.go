// Package version exposes the version string of the running build.
package version

// Version is the semantic version of this build. The release pipeline overrides
// it at link time, so the binary reports the git tag it was built from:
//
//	go build -ldflags "-X rqark-data-cloud/internal/version.Version=v1.2.3"
//
// A plain `go build` leaves the development default in place.
var Version = "dev"
