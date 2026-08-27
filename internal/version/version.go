// Package version holds build metadata injected at link time via
// -ldflags "-X github.com/xlip/multistream/internal/version.Version=...".
package version

// Version, Commit and Date are set by the release pipeline.
// Defaults apply to development builds.
var (
	Version = "dev"
	Commit  = "none"
	Date    = ""
)

// String renders the version line printed by -version.
func String() string {
	s := "multistream version " + Version
	if Commit != "none" {
		s += " (commit " + Commit
		if Date != "" {
			s += ", built " + Date
		}
		s += ")"
	}
	return s
}
