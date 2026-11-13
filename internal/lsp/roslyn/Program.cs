using System.Text.Json;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;

// Pedido:  {"id":1,"text":"class A {"}                 (o texto inteiro do arquivo)
// Resposta:{"id":1,"errors":[{"id":"CS1513","line":1,"col":10,"msg":"} esperado"}]}
// Só DiagnosticSeverity.Error. Warning, info e hidden nunca saem daqui.
// Idioma das mensagens: o do sistema operacional (o mesmo que o compilador mostraria).

var options = new JsonSerializerOptions { PropertyNamingPolicy = JsonNamingPolicy.CamelCase };
var stdin = Console.In;
var stdout = new StreamWriter(Console.OpenStandardOutput(), new System.Text.UTF8Encoding(false)) { AutoFlush = true };
var parseOptions = new CSharpParseOptions(LanguageVersion.Latest, DocumentationMode.None);

string? line;
while ((line = stdin.ReadLine()) != null)
{
    if (line.Length == 0) continue;
    int id = 0;
    try
    {
        using var doc = JsonDocument.Parse(line);
        var root = doc.RootElement;
        id = root.TryGetProperty("id", out var idEl) ? idEl.GetInt32() : 0;
        var text = root.GetProperty("text").GetString() ?? "";
        var tree = CSharpSyntaxTree.ParseText(text, parseOptions);
        var errors = tree.GetDiagnostics()
            .Where(d => d.Severity == DiagnosticSeverity.Error)
            .OrderBy(d => d.Location.SourceSpan.Start)
            .Take(50)
            .Select(d =>
            {
                var p = d.Location.GetLineSpan().StartLinePosition;
                return new { id = d.Id, line = p.Line + 1, col = p.Character + 1, msg = d.GetMessage() };
            })
            .ToArray();
        stdout.WriteLine(JsonSerializer.Serialize(new { id, errors }, options));
    }
    catch (Exception ex)
    {
        stdout.WriteLine(JsonSerializer.Serialize(new { id, errors = Array.Empty<object>(), fail = ex.Message }, options));
    }
}
