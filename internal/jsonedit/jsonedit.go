// Package jsonedit muda um membro de um objeto JSON preservando a ordem das outras chaves e os valores intactos.
// Usado pelo `fastexec setup` nos arquivos de config dos clientes (Claude Desktop, Claude Code, Antigravity...),
// que são do usuário: o arquivo é reindentado com 2 espaços, mas nada além do membro pedido muda de conteúdo.
package jsonedit

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type member struct {
	key string
	val json.RawMessage
}

// object é um objeto JSON com as chaves na ordem do arquivo.
type object []member

func parseObject(raw []byte) (object, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return object{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}
	var o object
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, member{key: kt.(string), val: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the JSON object")
	}
	return o, nil
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.val, true
		}
	}
	return nil, false
}

func (o object) set(key string, val json.RawMessage) object {
	for i, m := range o {
		if m.key == key {
			o[i].val = val
			return o
		}
	}
	return append(o, member{key: key, val: val})
}

func (o object) del(key string) object {
	for i, m := range o {
		if m.key == key {
			return append(o[:i], o[i+1:]...)
		}
	}
	return o
}

func (o object) encode() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// Get devolve o valor bruto em path (ex.: "mcpServers", "fastexec"); ok=false se algum nível não existe.
func Get(raw []byte, path ...string) (json.RawMessage, bool, error) {
	cur := json.RawMessage(raw)
	for _, k := range path {
		o, err := parseObject(cur)
		if err != nil {
			return nil, false, err
		}
		v, ok := o.get(k)
		if !ok {
			return nil, false, nil
		}
		cur = v
	}
	return cur, true, nil
}

// Set grava value (qualquer coisa que json.Marshal aceite, ou json.RawMessage) em path, criando os objetos
// intermediários que faltarem; value nil apaga o membro. Devolve o documento reindentado e terminado em '\n'.
func Set(raw []byte, value any, path ...string) ([]byte, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	var val json.RawMessage
	if value != nil {
		if r, ok := value.(json.RawMessage); ok {
			val = r
		} else {
			b, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			val = b
		}
	}
	out, err := setAt(raw, val, path)
	if err != nil {
		return nil, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out, "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

func setAt(raw []byte, val json.RawMessage, path []string) ([]byte, error) {
	o, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	k := path[0]
	if len(path) == 1 {
		if val == nil {
			return o.del(k).encode(), nil
		}
		return o.set(k, val).encode(), nil
	}
	child, _ := o.get(k)
	if bytes.Equal(bytes.TrimSpace(child), []byte("null")) {
		child = nil
	}
	nc, err := setAt(child, val, path[1:])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", k, err)
	}
	return o.set(k, nc).encode(), nil
}
