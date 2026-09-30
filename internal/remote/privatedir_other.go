//go:build !unix

package remote

func checkPrivateDir(string) error { return nil }
