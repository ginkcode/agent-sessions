//go:build !darwin

package manage

// defaultProcFS returns the procfs-backed process table used on Linux.
func defaultProcFS() ProcFS { return procDirFS{dir: "/proc"} }
