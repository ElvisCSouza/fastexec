// Sonda CS3 - contrato de LogEventProperty.IsValidName(name, throwOnInvalid).
// Compila so' se o parametro obrigatorio existir. As linhas provam tambem QUEM
// passa o que: os dois chamadores do Logger (ForContext/BindProperty) devolvem
// cedo em vez de lancar, entao tem que passar false; o caminho do construtor passa true.
using System;
using Serilog;
using Serilog.Events;

class Probe
{
    static void Main()
    {
        Console.WriteLine("CSOUT:valid=" + LogEventProperty.IsValidName("Nome", true));
        Console.WriteLine("CSOUT:blank=" + LogEventProperty.IsValidName("   ", false));

        var throws = false;
        try { LogEventProperty.IsValidName("  ", true); } catch (ArgumentException) { throws = true; }
        Console.WriteLine("CSOUT:throws=" + throws);

        var constrThrows = false;
        try { new LogEventProperty("  ", new ScalarValue(1)); } catch (ArgumentException) { constrThrows = true; }
        Console.WriteLine("CSOUT:constrthrows=" + constrThrows);

        var log = new LoggerConfiguration().CreateLogger();

        var kept = false;
        try { kept = ReferenceEquals(log.ForContext("  ", 1), log); } catch { kept = false; }
        Console.WriteLine("CSOUT:forcontext=" + kept);

        var bound = false;
        try
        {
            var ok = log.BindProperty("  ", 1, false, out var prop);
            bound = (ok == false) && (prop == null);
        }
        catch { bound = false; }
        Console.WriteLine("CSOUT:bindproperty=" + bound);

        var good = new LogEventProperty("Ok", new ScalarValue(7));
        Console.WriteLine("CSOUT:name=" + good.Name);
    }
}
