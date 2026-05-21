package version

import "fmt"

var (
	Version = "0.1.0-m0"
	Commit  = "dev"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("simple-sub2api %s (%s, %s)", Version, Commit, Date)
}
