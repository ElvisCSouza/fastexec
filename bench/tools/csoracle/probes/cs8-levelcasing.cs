// Sonda CS8 - casing/largura do elemento {Level}.
// LevelOutputFormat e' internal, entao a sonda entra pelo caminho publico
// (MessageTemplateTextFormatter) em vez de chamar a classe direto.
using System;
using System.Collections.Generic;
using System.IO;
using Serilog.Events;
using Serilog.Formatting.Display;

class Probe
{
    static string Render(string template)
    {
        var f = new MessageTemplateTextFormatter(template, null);
        var e = new LogEvent(DateTimeOffset.UtcNow, LogEventLevel.Warning, null, MessageTemplate.Empty, new List<LogEventProperty>());
        var sw = new StringWriter();
        f.Format(e, sw);
        return sw.ToString();
    }

    static void Main()
    {
        Console.WriteLine("CSOUT:w3=" + Render("{Level:w3}"));
        Console.WriteLine("CSOUT:u3=" + Render("{Level:u3}"));
        Console.WriteLine("CSOUT:t3=" + Render("{Level:t3}"));
        Console.WriteLine("CSOUT:w2=" + Render("{Level:w2}"));
        Console.WriteLine("CSOUT:w5=" + Render("{Level:w5}"));
        Console.WriteLine("CSOUT:full=" + Render("{Level}"));
    }
}
