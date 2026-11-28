//go:build !windows

package core

import "os"

func createDirLink(string, string) error {
	return os.ErrPermission
}
