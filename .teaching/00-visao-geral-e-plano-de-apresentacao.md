# Trilha Educacional FastExec: Do Conceito à Apresentação Executiva

> **Público-alvo:** Diretores de Tecnologia, Engenheiros-Chefe de IA, Arquitetos de Software, Líderes de SRE e FinOps.  
> **Objetivo:** Fornecer o embasamento conceitual, econômico e arquitetural para defender por que agentes de IA em produção exigem um **Runtime Cognitivo Dedicado**, e por que abordagens ingênuas (dar "terminal/bash puro" para a LLM) são caras, instáveis e perigosas.

---

## 1. A Tese Central da Apresentação

> *"Um modelo de linguagem não é um desenvolvedor humano sentado num teclado. Conectar uma LLM a um terminal genérico via SSH ou Bash comum é uma falha de design arquitetural que drena orçamentos em tokens, causa travamentos por timeout e gera vazamentos de processos. O agente precisa de um **Runtime de Mediação Cognitiva**."*

---

## 2. Estrutura dos Módulos Educacionais

A pasta `.teaching` está dividida em 6 blocos temáticos modulares, pensados para serem lidos de forma independente ou combinados em uma palestra/keynote:

| Módulo | Arquivo | Pergunta que responde | Foco Conceitual |
| :--- | :--- | :--- | :--- |
| **01** | `01-o-paradigma-runtime-cognitivo-vs-terminal.md` | *Por que não plugar a LLM direto no Bash/SSH?* | A mudança de paradigma: Shell Passivo vs Runtime Ativo; a barreira da interface humano-máquina. |
| **02** | `02-finops-e-engenharia-de-contexto.md` | *Como economizar 40% a 60% de custos com tokens?* | FinOps de IA; *Content-Aware Minification*; compressão de tabelas sem quebrar código; Tool RAG. |
| **03** | `03-auto-remediacao-e-pedagogia-de-agentes.md` | *Por que falhas secas causam loops infinitos?* | Auto-Remediação em tempo de execução; a psicologia dos agentes; correção com feedback instrutivo (*nudges*). |
| **04** | `04-governanca-de-processos-e-resiliencia.md` | *Como evitar travamentos e processos zumbis?* | O limite de 60s em clientes de IA; morte atômica de processos via Windows Job Objects; ambiente Headless Canônico. |
| **05** | `05-arquitetura-de-transporte-e-latencia.md` | *Por que cada milissegundo de overhead importa?* | Efeito cumulativo de latência em cadeias de raciocínio (*tool chains*); sockets diretos em Zig/Go vs túneis lentos. |
| **06** | `06-deck-de-slides-para-apresentacao.md` | *Como apresentar isso para um time ou diretoria?* | Roteiro completo de apresentação (slide a slide) com ganchos narrativos, diagramas e conclusões para palco. |

---

## 3. O Pitch de 60 Segundos (Elevator Pitch para Diretoria)

> "No mercado de IA generativa, gastamos milhões tentando treinar ou afinar modelos maiores para resolver problemas complexos, mas ignoramos a camada onde o agente realmente toca a infraestrutura.  
> Quando uma LLM roda um comando, um terminal clássico devolve 50 KB de lixo visual, barras de progresso `\r`, avisos de update e espaços em branco que entram direto na janela de contexto cobrando dólares em tokens por minuto. Além disso, se o comando passar de 60 segundos, a sessão cai; se o comando falhar, o modelo tenta adivinhar em loop; e se o processo travar, netos órfãos continuam consumindo CPU do servidor.  
> O FastExec não foi feito para 'dar shell'; ele foi feito para ser a **camada de governança e filtro cognitivo** entre a mente da IA e a máquina. Ele corta até 60% dos tokens desperdiçados, elimina processos zumbis no kernel, intercepta e educa maus hábitos do modelo em milissegundos e garante resiliência operacional contínua."
