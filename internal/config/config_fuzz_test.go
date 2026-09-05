package config

import "testing"

func FuzzParseTOML(f *testing.F) {
	f.Add([]byte("[server]\npublic_addr = \"127.0.0.1:8080\"\n"))
	f.Add([]byte("[auth]\nmode = \"disabled\"\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = ParseTOML(input)
	})
}
