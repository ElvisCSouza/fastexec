//go:build !windows

package env

import "os"

type RegistryEnv struct {
	Machine map[string]string `json:"m"`
	User    map[string]string `json:"u"`
}

// ReadRegistry em sistemas POSIX (Linux/macOS) não usa registro do Windows; retorna mapa vazio sem erro.
func ReadRegistry() (*RegistryEnv, error) {
	return &RegistryEnv{
		Machine: make(map[string]string),
		User:    make(map[string]string),
	}, nil
}

func ExpandSZ(s string, env map[string]string) string {
	return os.Expand(s, func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return ""
	})
}
