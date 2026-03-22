package serviceutils

import "goMH/config"

func DiscoverLogDirectories(cfg *config.Config) []string {
	return (&Module{}).findLogDirectories(cfg)
}
