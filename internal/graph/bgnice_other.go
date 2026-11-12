//go:build !windows

package graph

// bgnice_other.go: fora do Windows a passada de fundo não mexe em prioridade (nice por thread é específico de
// plataforma e a goroutine pode trocar de thread); o que limita o impacto lá é o semáforo de uma passada por vez.
func lowerThreadPriority() (restore func()) { return func() {} }
