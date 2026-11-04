# Módulo 06: Deck de Slides — Roteiro Completo para Apresentação Executiva

> **Formato:** Estrutura pronta para ser copiada para PowerPoint, Google Slides, Keynote ou apresentação em Markdown (Marp / Slidev).  
> **Apresentador:** Diretor de Tecnologia / Líder Técnico / Arquiteto-Chefe.  
> **Duração estimada:** 25 a 35 minutos + Perguntas.

---

### Slide 1: Capa & Gancho de Abertura
* **Título:** Além do Terminal: Por que Agentes de IA em Produção Exigem um Runtime Cognitivo
* **Subtítulo:** Como transformar a execução de ferramentas de um ralo de tokens e falhas operacionais em uma vantagem competitiva de latência e custo.
* **Apresentador:** [Seu Nome], Diretor de Tecnologia
* **Notas do Apresentador (Fala):**
  > "Hoje nós investimos pesado na inteligência dos modelos: compramos os maiores checkpoints, construímos prompts elaborados e discutimos arquiteturas multiagente. Mas quase ninguém está olhando para a camada onde o agente realmente toca o mundo real: a ferramenta de execução. O que vou mostrar hoje é por que plugar uma LLM diretamente em um terminal Bash ou SSH é uma falha arquitetural grave — e como resolvemos isso com uma nova abordagem: o Runtime Cognitivo."

---

### Slide 2: A Ilusão do "Cabo Solto"
* **Título:** O Anti-Pattern: A LLM Plugada no Shell Passivo
* **Visual:** Diagrama de uma LLM conectada por um "cabo cego" a um terminal Linux/Windows cheio de caracteres ANSI borrados.
* **Pontos-Chave:**
  * O terminal foi projetado para os olhos de um humano em 1975, não para o transformer de 2026.
  * O modelo consome tokens para ler cores ANSI, spinners de download e quebras de linha inócuas.
  * Se o comando faz uma pergunta interativa (`[y/n]`), a chamada trava.
  * Se o comando passa de 60 segundos, a sessão do cliente MCP cai e a conversa morre.
* **Notas do Apresentador (Fala):**
  > "Tratar uma ferramenta de execução como um simples 'cabo passivo' que joga stdout bruto de volta para o modelo custa caro, é instável e gera frustração. A IA não precisa de uma emulação de terminal; ela precisa de fatos processados e governança estrita."

---

### Slide 3: O Ralo Invisível de Tokens (FinOps)
* **Título:** A Economia Invisível dos Tokens: Onde seu Orçamento Morre
* **Visual:** Gráfico de quebra de tokens: 60% lixo de terminal (ANSI, `\r`, whitespace de tabelas, banners de update) vs. 40% informação útil.
* **Destaques:**
  * Barras de progresso (`curl`, `pip`, `npm`) despejam centenas de frames repetidos em texto puro.
  * Tabelas de sistema (`docker ps`, `df -h`) são 35% formadas por espaços em branco vazios.
  * Payloads JSON humanos contêm 50% de overhead em quebras de linha e recuos estéticos.
* **Notas do Apresentador (Fala):**
  > "Cada espaço em branco e cada barra de progresso que a CLI cospe é faturado na API do modelo. Em uma empresa com dezenas de agentes e centenas de chamadas por dia, estamos pagando milhares de dólares por mês para fazer a LLM ler espaços em branco."

---

### Slide 4: A Solução 1 — Content-Aware Minification
* **Título:** Minificação Inteligente com Preservação Sintática
* **Visual:** Antes e Depois:
  * *Antes:* JSON formatado e tabela inflada (3.800 tokens).
  * *Depois:* JSON compacto inline e colunas colapsadas para 2 espaços (1.100 tokens — **-71% de custo**).
* **O Segredo Técnico:**
  * O runtime detecta código-fonte, diffs e YAML e **preserva 100% da indentação intacta**.
  * Em saídas de CLI pura, colapsa vãos vazios (`\s{3,} -> 2 espaços`).
  * Emula o cursor e resolve `\r`, entregando apenas o estado final da barra de progresso.
* **Notas do Apresentador (Fala):**
  > "Criamos uma esteira determinística em Go que limpa a saída em menos de 1 milissegundo. Ela sabe a diferença entre um script Python (onde um espaço a menos quebra a sintaxe) e uma tabela de processos de sistema. Economizamos mais de 50% dos tokens de saída sem risco de corrupção."

---

### Slide 5: A Solução 2 — Auto-Remediação Pedagógica
* **Título:** Em Vez de Falhas Secas, Educação em Runtime
* **Visual:** Comparativo entre o "Modo Rejeição" (que gera loops infinitos de erro) e o "Modo Auto-Remediação" do FastExec.
* **Casos Reais:**
  * `cat arquivo` $\longrightarrow$ Interceptado em 0ms; lido via memória nativa; devolvido com o conteúdo + Nudge instrutivo.
  * `sleep 10` $\longrightarrow$ Interceptado no Go; resolvido sem processos extras; devolvido com dica de healthcheck.
  * Sintaxes erradas de SQL $\longrightarrow$ Devolve dica contextual de correção sem abortar o raciocínio.
* **Notas do Apresentador (Fala):**
  > "Quando uma ferramenta diz apenas 'Comando Inválido', a LLM entra em pânico e começa a tentar comandos aleatórios, queimando turnos. O FastExec resolve a intenção na hora, entrega o resultado e insere um aviso pedagógico no topo. O modelo aprende sem parar o trabalho."

---

### Slide 6: A Solução 3 — Governança no Kernel e Fim dos Órfãos
* **Título:** Resiliência Operacional: O Kernel a Serviço do Agente
* **Visual:** Diagrama de Windows Job Objects (`KILL_ON_JOB_CLOSE`) encerrando Pai, Filhos e Netos simultaneamente.
* **Pontos Críticos:**
  * **O Teto dos 45s:** Interrompe a execução aos 45 segundos, garantindo 15s de folga para o limite de 60s do Claude Desktop. A sessão do usuário nunca é destruída.
  * **Morte Atômica de Árvores:** Nada de matar por PID (o que arrisca matar processos de terceiros no Windows). O Job Object limpa tudo no kernel quando a chamada fecha.
  * **Ambiente Headless Canônico:** Injeção mandatória de `CI=1`, `PAGER=cat`, `NO_COLOR=1`. Zero risco de a ferramenta abrir um paginador `less` ou esperar `[y/n]`.
* **Notas do Apresentador (Fala):**
  > "Nenhum subprocesso neto sobrevive a uma chamada do FastExec. Se o cliente abortar ou der timeout, a árvore inteira evapora no kernel. Além disso, ao cortar em 45s, salvamos o chat do usuário do timeout fatal de 60s que as ferramentas comuns ignoram."

---

### Slide 7: A Solução 4 — Latência Sub-Segundo (Go + Zig)
* **Título:** Transporte Multihost: Eliminando a Fricção Mecânica
* **Visual:** Comparação de latência: SSH Tradicional (600ms) vs. FastExec Socket (2ms).
* **Arquitetura da Malha:**
  * **Local (Windows):** Daemon residente Go (`:9146`), compartilhado entre várias IAs sem abrir consoles na tela.
  * **Remoto (Linux e Roteadores):** Daemons estáticos em Zig (`:9142`) de 400 KB e <2 MB de RAM.
  * **Cadeias de Ferramentas:** Em 12 chamadas consecutivas, economiza-se mais de 8 segundos de espera pura.
* **Notas do Apresentador (Fala):**
  > "Em tarefas complexas, o agente precisa encadear 10 ou 15 ações. Se cada ação gasta 600ms só de handshake SSH e cold start de shell, a experiência fica pesada. Com sockets diretos e binários nativos em Zig e Go, derrubamos o transporte para 2 milissegundos."

---

### Slide 8: A Padronização de Shell & Encoding
* **Título:** O Fim do Pesadelo do UTF-16LE em Ambientes Mistos
* **Visual:** Como o FastExec unificou o ambiente operacional Windows/Linux.
* **Destaques:**
  * Priorização automática de **PowerShell 7 (`pwsh`)** sobre o Windows PowerShell 5.1 legado.
  * Fim de saídas corrompidas: arquivos gerados em **UTF-8 nativo sem BOM**.
  * Expansão universal de `~`: caminhos funcionam igualmente no Linux (`/home/elvis`) e no Windows (`C:\Users\ElvisSouza`).
* **Notas do Apresentador (Fala):**
  > "Eliminamos uma das causas mais comuns de falhas silenciosas em agentes corporativos: ferramentas Windows gravando saídas em UTF-16LE com BOM que quebram bibliotecas em Node ou Go. Padronizamos tudo em UTF-8 puro e garantimos consistência de ambiente."

---

### Slide 9: O Impacto Consolidado (Tabela de Resultados)

| Métrica | Ferramentas de Mercado (Cabo Passivo) | Com o FastExec Runtime | Ganho Obtido |
| :--- | :--- | :--- | :--- |
| **Custo de Tokens em Saídas** | 100% (saídas brutas de CLI) | 40% a 55% | **-45% a -60% em tokens** |
| **Overhead de System Prompt** | ~900 tokens (catálogo estático) | ~490 tokens (Tool RAG dinâmico) | **-45,5% por turno** |
| **Latência por Invocação** | 350 ms a 1.200 ms | 1 ms a 4 ms | **~99% mais rápido** |
| **Taxa de Sessões Mortas por Timeout** | Frequente em builds (> 60s) | **Zero** (Corte em 45s com saída parcial) | **100% de resiliência** |
| **Processos Órfãos / Zumbis** | Comum ao abortar scripts | **Zero** (Job Objects Win32 / cgroups) | **Zero vazamento de CPU** |

---

### Slide 10: Princípios de Design para Lideranças Técnicas
* **Título:** 4 Lições para a Arquitetura de Sistemas Agenticos
1. **O Transporte é Apenas o Começo:** Não confunda dar acesso a comandos com fornecer um runtime de execução preparado para IA.
2. **FinOps se Faz no Byte:** Minificar e limpar saídas de ferramentas é mais barato e eficaz do que tentar diminuir o tamanho dos modelos.
3. **Eduque em Tempo Real:** O melhor momento para treinar um modelo de linguagem a usar suas ferramentas é no exato instante em que ele executa a ação errada.
4. **Governança no Sistema Operacional:** Seus guardrails de segurança e limites de timeout devem viver no kernel da máquina, não apenas no prompt da LLM.

---

### Slide 11: Encerramento & Q&A
* **Título:** FastExec — Inteligência na Camada de Execução
* **Contato:** [Seu Nome / Cargo]
* **Chamada para Ação:** Perguntas e discussão de arquitetura.
