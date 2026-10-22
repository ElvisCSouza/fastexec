package tools

import (
	"encoding/base64"
	"fmt"

	"homelab/commander/internal/transport"
)

type SqlArgs struct {
	Host     string `json:"host"`
	Database string `json:"database"`
	Query    string `json:"query"`
}

func HandleSql(args SqlArgs) (string, error) {
	if args.Database == "" {
		return "", fmt.Errorf("parâmetro 'database' é obrigatório")
	}
	if args.Query == "" {
		return "", fmt.Errorf("parâmetro 'query' é obrigatório")
	}

	b64Query := base64.StdEncoding.EncodeToString([]byte(args.Query))
	cmd := fmt.Sprintf("echo '%s' | base64 -d | sqlite3 -header -column '%s'", b64Query, args.Database)

	res, err := transport.Dispatch(args.Host, cmd, transport.DefaultTimeout)
	if err != nil {
		return "", err
	}

	if res.ExitCode != 0 {
		return fmt.Sprintf("[SQL Error: exit %d] %s", res.ExitCode, res.Stderr), fmt.Errorf("sql falhou com código %d", res.ExitCode)
	}

	if res.Stdout == "" {
		return "(0 linhas retornadas / query executada com sucesso)", nil
	}

	return res.Stdout, nil
}
