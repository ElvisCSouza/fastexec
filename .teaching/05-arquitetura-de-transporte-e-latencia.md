# Módulo 05: Arquitetura de Transporte & Latência — A Fricção Sub-Segundo

> *"Uma diferença de 300 milissegundos não parece grande coisa quando um humano está digitando comandos manualmente. Mas quando um agente autônomo executa uma cadeia de 12 chamadas de ferramentas consecutivas para resolver um bug, o overhead do transporte se multiplica por doze, degradando a experiência do usuário de interativa para intolerável."*

---

## 1. O Efeito de Amplificação de Latência em Cadeias de Raciocínio (*Tool Chains*)

Agentes modernos não executam apenas um comando e param. Eles trabalham em loops de percepção-ação:
$$\text{Planejar} \longrightarrow \text{Ferramenta 1} \longrightarrow \text{Avaliar} \longrightarrow \text{Ferramenta 2} \longrightarrow \dots \longrightarrow \text{Conclusão}$$

Em uma tarefa típica de refatoração ou diagnóstico, o modelo pode disparar entre **8 e 15 chamadas de ferramentas em sequência**.

### Comparativo de Acúmulo de Latência:

| Camada de Transporte | Latência por Invocação | Tempo Perdido em 12 Chamadas (Só no Transporte) |
| :--- | :--- | :--- |
| **SSH Tradicional (com handshake TLS e fork de bash)** | ~450 ms a 900 ms | **5,4 a 10,8 segundos** |
| **Windows PowerShell 5.1 Frio (.NET monolítico)** | ~250 ms a 400 ms | **3,0 a 4,8 segundos** |
| **FastExec Local (Daemon Go :9146)** | **~1 ms a 3 ms** | **0,02 segundo** |
| **FastExec Remoto (Daemon Zig :9142)** | **~2 ms a 5 ms** | **0,04 segundo** |

> **Impacto:** O FastExec reduz a latência acumulada de transporte de **segundos inteiros para milissegundos imperceptíveis**, fazendo com que o gargalo do sistema seja unicamente o tempo de geração do modelo.

---

## 2. A Escolha das Tecnologias: Por que Go e Zig?

Muitos frameworks de agentes são escritos em Python ou TypeScript/Node.js.  
Por que o FastExec adotou uma arquitetura híbrida de **Go no cliente/mediador** e **Zig nos daemons remotos**?

```
[Visão Geral da Malha de Transporte FastExec]

              ┌────────────────────────────────────────┐
              │     FastExec Orquestrador (Go)         │
              │  PC-Elvis (Daemon Local 127.0.0.1:9146) │
              └───────────────┬────────────────────────┘
                              │
         ┌────────────────────┼────────────────────┐
         │ (TCP Socket :9142) │ (TCP Socket :9142) │ (TCP Socket :9142)
         ▼                    ▼                    ▼
   [ NoteServer ]        [ AX6000 ]           [ AX3000 ]
   (Linux x86_64)      (Roteador aarch64)   (Roteador armv7l)
   Zig server-exec     Zig server-exec      Zig server-exec
   (Binário 400 KB)    (Binário 400 KB)     (Binário 400 KB)
```

### 1. No PC Local: Go com Daemon Residente (`127.0.0.1:9146`)
* **Zero Console Popups:** Um problema clássico no Windows é que executar comandos de fundo ou scripts frequentemente faz o terminal "piscar" na tela do desenvolvedor. O FastExec roda compilado com flag `-H=windowsgui` acoplado ao daemon residente, garantindo execução 100% silenciosa.
* **Multi-IA & Multi-Sessão:** Antigravity, Claude Desktop, OpenClaw e terminais de linha de comando podem conversar simultaneamente com o mesmo daemon residente local, reaproveitando conexões e mantendo o consumo de memória abaixo de 15 MB.

### 2. Nos Servidores e Roteadores: O Daemon Zig (`server-exec`)
Para os nós remotos (`NoteServer`, roteadores Xiaomi AX6000 e AX3000 com OpenWrt), o FastExec não abre sessões SSH lentas. Ele conversa diretamente via TCP socket com um binário estático escrito em **Zig**:
* **Por que Zig?**
  * Binário estático minúsculo (~400 KB), sem dependências dinâmicas de glibc.
  * Consumo de RAM inferior a **2 MB** (pode rodar confortavelmente em microcontroladores e roteadores domésticos com memória escassa).
  * Inicialização e fork de processos em microssegundos.
  * Dispensa autenticações repetidas de chaves SSH a cada linha de comando executada.

### 3. Em Aparelhos Android / Firestick: Ponte de Baixo Overhead
* Nos dispositivos de mídia (Firestick 4K Max), o daemon Zig roda nativo com socket TCP direto e bridge ADB mantida viva (*keep-alive*). Isso reduz o tempo de deploy de APKs ou testes na TV de **50 segundos para menos de 2 milissegundos**.

---

## 3. A Padronização de Encoding e o Fim do UTF-16LE

Em ambientes mistos Windows/Linux, outro causador invisível de falhas graves é a codificação de caracteres:
* O **Windows PowerShell 5.1** legado utiliza por padrão o redirecionamento (`>` e `Out-File`) gravando arquivos em **UTF-16LE com Byte Order Mark (BOM)**.
* Quando um agente ou script em Node.js, Go ou Python tenta ler esse arquivo gravado em UTF-16LE como se fosse UTF-8, strings com acentos quebram, o parser de JSON falha e o agente entra em loop de erro.
* **A solução FastExec:**
  * O FastExec resolve e prioriza automaticamente o **PowerShell 7 (`pwsh`)**, que é nativo em **UTF-8 limpo sem BOM**.
  * Desvia das armadilhas da sandbox UWP da Microsoft Store, localizando o binário real de sistema.
  * Mantém decodificador adaptativo de páginas de código OEM (850/CP1252) no Windows, garantindo que saídas com acentuação nunca virem caracteres corrompidos (`\ufffd`).

---

## 4. Conclusão para o Slide Executivo

> **Takeaway de Arquitetura de Redes:**  
> "A latência da ferramenta não é apenas uma métrica de rede; ela define o teto da capacidade iterativa do agente. Ao eliminar handshakes de SSH e inicializações de runtimes pesados em favor de sockets diretos e daemons compilados estaticamente, removemos a fricção mecânica do sistema."
