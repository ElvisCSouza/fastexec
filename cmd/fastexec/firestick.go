package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Comandos do app FastExec nos Firesticks (porta 9143, uma linha de texto, uma linha de resposta).
// Shell no Firestick nao passa por aqui: use `fastexec exec firestick-sala "<comando>"` (porta 9142).
var firesticks = map[string]string{
	"sala":  "192.168.31.21:9143",
	"suite": "192.168.31.37:9143",
}

const firestickUso = `Uso: fastexec firestick <sala|suite> <comando>
  adb on | adb status | adb off      ADB e PERMANENTE (sempre ON). 'adb off' mata o root e apaga a autorizacao: nao usar
  autotv on | autotv off | autotv    Home ao acordar (liga a TV por HDMI-CEC). Sala: off
  state | wake | sleep | home | back | app | launch <pacote|url> | report
Shell no aparelho: fastexec exec firestick-sala "logcat -d -t 200"`

func runFirestick(args []string) {
	if len(args) < 2 {
		fmt.Println(firestickUso)
		os.Exit(1)
	}
	addr, ok := firesticks[strings.ToLower(args[0])]
	if !ok {
		fmt.Println(firestickUso)
		os.Exit(1)
	}
	line := strings.Join(args[1:], " ")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "firestick %s: sem conexao com o app (%v)\n", args[0], err)
		os.Exit(1)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte(line + "\n")); err != nil {
		fmt.Fprintf(os.Stderr, "firestick %s: falha ao enviar (%v)\n", args[0], err)
		os.Exit(1)
	}
	resp, err := bufio.NewReader(conn).ReadString('\n')
	resp = strings.TrimSpace(resp)
	if resp == "" && err != nil {
		fmt.Fprintf(os.Stderr, "firestick %s: sem resposta (%v)\n", args[0], err)
		os.Exit(1)
	}
	fmt.Println(resp)
	if strings.HasPrefix(resp, "FAIL") || strings.HasPrefix(resp, "ERR") {
		os.Exit(1)
	}
}
