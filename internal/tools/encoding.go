package tools

import "homelab/fastexec/internal/textenc"

// Codificações de arquivo de texto que o modelo não consegue ler como UTF-8. Legados brasileiros (Windows-1252 / Latin-1)
// e o UTF-16 que o Windows PowerShell 5 grava por padrão chegavam ao modelo como "Informa��o", e ele recorria a
// scripts (python que o host nem tem) só para decodificar: turnos e erros gastos por nada.
//
// A detecção e a conversão vivem em internal/textenc, compartilhadas com o aplicador de
// WorkspaceEdit do LSP — duas implementações divergentes gravariam arquivo corrompido.
const (
	encWindows1252 = textenc.Windows1252
	encUTF16LE     = textenc.UTF16LE
	encUTF16BE     = textenc.UTF16BE
)

// decodeLegacyText devolve o texto em UTF-8 e o nome da codificação de origem. Sem conversão (UTF-8 válido, binário ou
// vazio) devolve o próprio dado e "". Binário (NUL fora de UTF-16 com BOM) nunca é "decodificado".
func decodeLegacyText(data string) (string, string) { return textenc.Decode(data) }

// encodeLike converte texto UTF-8 de volta para a codificação detectada na leitura. ok=false se algum caractere não
// cabe na codificação (a edição não deve gravar "?" no lugar).
func encodeLike(text, enc string) (string, bool) { return textenc.Encode(text, enc) }
