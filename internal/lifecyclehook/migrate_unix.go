//go:build !windows

package lifecyclehook

func migratedArgv(command string) ([]string, error) {
	return []string{"sh", "-c", command}, nil
}
