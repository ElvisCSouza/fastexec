// Sonda CS4 - o metodo antigo (Write+Line) foi renomeado para SelfLog.Write.
// Compila so' com o nome novo. O chute classico (renomear WriteLine->Write tambem
// dentro do lambda de Enable(TextWriter)) compila e "funciona", mas perde o fim de
// linha: por isso 'newline' e 'lines' entram no gabarito.
using System;
using System.IO;
using Serilog.Debugging;

class Probe
{
    static void Main()
    {
        var sw = new StringWriter();
        SelfLog.Enable(sw);
        try { SelfLog.Write("problema {0}", 42); } finally { SelfLog.Disable(); }
        var s = sw.ToString();
        Console.WriteLine("CSOUT:hasmsg=" + s.Contains("problema 42"));
        Console.WriteLine("CSOUT:newline=" + s.EndsWith(Environment.NewLine));
        Console.WriteLine("CSOUT:lines=" + s.Split(new[] { Environment.NewLine }, StringSplitOptions.RemoveEmptyEntries).Length);
    }
}
