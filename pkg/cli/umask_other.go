//go:build !unix

package cli

func umask(mask int) int { return 0 }
