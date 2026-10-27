// Package version is set at build time with -ldflags -X.
package version

var (
	Version = "dev"
	Commit  = "none"
)

func String() string {
	if Commit == "none" || Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
