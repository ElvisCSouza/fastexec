//go:build windows

package env

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

type RegistryEnv struct {
	Machine map[string]string `json:"m"`
	User    map[string]string `json:"u"`
}

func readKeyValues(k registry.Key, path string) (map[string]string, error) {
	key, err := registry.OpenKey(k, path, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()

	names, err := key.ReadValueNames(0)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(names))
	for _, name := range names {
		val, valType, err := key.GetStringValue(name)
		if err != nil {
			continue
		}
		if valType == registry.EXPAND_SZ {
			if expanded, err := registry.ExpandString(val); err == nil {
				val = expanded
			}
		}
		result[name] = val
	}
	return result, nil
}

// ReadRegistry lê as variáveis de ambiente diretamente da API Win32 do Registro (HKLM e HKCU),
// sem disparar subprocessos externos. Expande REG_EXPAND_SZ automaticamente.
func ReadRegistry() (*RegistryEnv, error) {
	machine, errM := readKeyValues(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)
	if errM != nil {
		machine = make(map[string]string)
	}

	user, errU := readKeyValues(registry.CURRENT_USER, `Environment`)
	if errU != nil {
		user = make(map[string]string)
	}

	if errM != nil && errU != nil {
		return nil, errM
	}

	return &RegistryEnv{
		Machine: machine,
		User:    user,
	}, nil
}

// ExpandSZ simula REG_EXPAND_SZ: %VAR% é expandido com os valores do env
func ExpandSZ(s string, env map[string]string) string {
	return os.Expand(s, func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return ""
	})
}
