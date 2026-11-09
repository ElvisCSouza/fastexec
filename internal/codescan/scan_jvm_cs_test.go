package codescan

import (
	"fmt"
	"strings"
	"testing"
)

func outline(t *testing.T, name, src string) string {
	t.Helper()
	l := LangFor(name)
	if l == nil {
		t.Fatalf("no language for %s", name)
	}
	ds, _ := Scan([]byte(src), l)
	var b strings.Builder
	for _, d := range ds {
		fmt.Fprintf(&b, "%d-%d %s %s\n", d.Line, d.EndLine, d.Kind, d.Name)
	}
	return b.String()
}

func TestCSharp(t *testing.T) {
	src := `using System;
namespace Size.Legado
{
    [Serializable]
    public sealed partial class PedidoService : IPedidoService
    {
        private readonly int _x;
        public PedidoService(int x)
        {
            _x = x;
        }
        public async Task<List<Pedido>> ListarAsync(int clienteId, CancellationToken ct)
        {
            if (clienteId > 0) { return await Buscar(clienteId); }
            return new List<Pedido>();
        }
        private static string Formatar(Pedido p) => p.ToString();
        public int Total { get; set; }
    }
    public record PedidoDto(int Id);
    internal enum Status { Aberto, Fechado }
}
`
	got := outline(t, "a.cs", src)
	for _, want := range []string{"5-19 class PedidoService", "8-11 method PedidoService", "12-16 method ListarAsync", "20-20 record PedidoDto", "21-21 enum Status"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"Formatar", "Total", "Buscar", " if"} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, got)
		}
	}
}

func TestKotlin(t *testing.T) {
	src := `package br.domo.tv
import android.os.Bundle

data class Canal(val id: Int, val nome: String)

class MainActivity : AppCompatActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val s = """
            fun naoEDeclaracao() {
        """
    }

    private suspend fun carregar(id: Int): Canal {
        return api.buscar(id)
    }

    companion object {
        const val TAG = "x"
    }
}

fun String.normalizar(): String = trim().lowercase()
typealias Lista = List<Canal>
`
	got := outline(t, "a.kt", src)
	for _, want := range []string{"4-4 class Canal", "6-21 class MainActivity", "7-12 method onCreate", "14-16 method carregar", "23-23 func normalizar", "24-24 type Lista"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "naoEDeclaracao") {
		t.Errorf("declaration inside a string:\n%s", got)
	}
}

func TestJava(t *testing.T) {
	src := `package x;
import java.util.List;

public class Player extends Base {
    private int n;

    public Player(int n) {
        this.n = n;
    }

    @Override
    public void play(String url) throws Exception {
        if (url == null) { return; }
        start(url);
    }

    private static List<String> parse(String s) {
        return List.of(s);
    }
}
`
	got := outline(t, "a.java", src)
	for _, want := range []string{"4-20 class Player", "7-9 method Player", "12-15 method play", "17-19 method parse"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
