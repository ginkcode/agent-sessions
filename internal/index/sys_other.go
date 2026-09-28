//go:build !(unix || linux || darwin)

package index

func setPrivateUmask() func() {
	return func() {}
}
