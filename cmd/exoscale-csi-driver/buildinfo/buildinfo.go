// Package buildinfo holds build metadata that was set through `-ldflags` in the main package.
package buildinfo

var (
	Version   string
	GitCommit string
	BuildDate string
)
