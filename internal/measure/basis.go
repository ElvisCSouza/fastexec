package measure

import "sort"

// Basis diz de onde vem o número de uma economia.
type Basis int

const (
	// Counterfactual: depende do que o modelo faria sem o FastExec (ler o arquivo inteiro, tentar de novo...).
	// É uma hipótese, não uma observação.
	Counterfactual Basis = iota
	// Measured: bytes que o FastExec removeu de um texto que a própria chamada produziu. Observado.
	Measured
)

func (b Basis) String() string {
	if b == Measured {
		return "medido"
	}
	return "estimativa"
}

// BasisPolicy classifica uma categoria de economia (estratégia).
type BasisPolicy interface {
	Basis(cat string) Basis
}

// CatPolicy é a política por tabela. Categoria desconhecida é Counterfactual: nunca infla o medido.
type CatPolicy struct{ byCat map[string]Basis }

// NewCatPolicy cria uma política vazia.
func NewCatPolicy() *CatPolicy { return &CatPolicy{byCat: map[string]Basis{}} }

// Register define a base de uma categoria; uma categoria nova se registra aqui, sem editar o relatório.
func (p *CatPolicy) Register(cat string, b Basis) *CatPolicy {
	p.byCat[cat] = b
	return p
}

// Basis implementa BasisPolicy.
func (p *CatPolicy) Basis(cat string) Basis {
	if b, ok := p.byCat[cat]; ok {
		return b
	}
	return Counterfactual
}

// DefaultPolicy classifica as categorias do FastExec hoje.
func DefaultPolicy() *CatPolicy {
	return NewCatPolicy().
		Register("clean", Measured).
		Register("cut", Measured).
		Register("group", Measured).
		Register("dedup", Measured).
		Register("readcap", Measured).
		Register("slice", Counterfactual).
		Register("retry", Counterfactual).
		Register("timeout", Counterfactual).
		Register("schema", Counterfactual).
		Register("edit", Counterfactual)
}

// CatTotal é a economia de uma categoria na janela.
type CatTotal struct {
	Cat    string
	Basis  Basis
	Bytes  int64
	Tokens int64
	Events int64
	Trips  int64
}

// SavingsSplit separa a economia em medida e estimada.
type SavingsSplit struct {
	Cats            []CatTotal // ordem decrescente de bytes
	MeasuredTokens  int64
	EstimatedTokens int64
}

// SplitSavings soma as linhas por categoria e separa pelo que a política diz.
func SplitSavings(rows []SavingRow, p BasisPolicy) SavingsSplit {
	idx := map[string]int{}
	var s SavingsSplit
	for _, r := range rows {
		i, ok := idx[r.Cat]
		if !ok {
			i = len(s.Cats)
			idx[r.Cat] = i
			s.Cats = append(s.Cats, CatTotal{Cat: r.Cat, Basis: p.Basis(r.Cat)})
		}
		c := &s.Cats[i]
		c.Bytes += r.Bytes
		c.Events++
		c.Trips += r.Trips
	}
	for i := range s.Cats {
		c := &s.Cats[i]
		c.Tokens = tokensOf(c.Bytes)
		if c.Basis == Measured {
			s.MeasuredTokens += c.Tokens
		} else {
			s.EstimatedTokens += c.Tokens
		}
	}
	sort.SliceStable(s.Cats, func(i, j int) bool { return s.Cats[i].Bytes > s.Cats[j].Bytes })
	return s
}
