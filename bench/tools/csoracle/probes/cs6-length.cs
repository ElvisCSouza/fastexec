// Sonda CS6 - fronteiras dos validadores de comprimento do FluentValidation.
// O bug plantado (comparacao '<= min' em vez de '< min') so' aparece nas
// fronteiras: MinimumLength(n) com exatamente n caracteres e ExactLength(n).
using System;
using FluentValidation;

public class Alvo { public string Nome { get; set; } }

public class ValMin : AbstractValidator<Alvo> { public ValMin() { RuleFor(x => x.Nome).MinimumLength(3); } }
public class ValMax : AbstractValidator<Alvo> { public ValMax() { RuleFor(x => x.Nome).MaximumLength(5); } }
public class ValExato : AbstractValidator<Alvo> { public ValExato() { RuleFor(x => x.Nome).ExactLength(4); } }
public class ValFaixa : AbstractValidator<Alvo> { public ValFaixa() { RuleFor(x => x.Nome).Length(2, 4); } }

class Probe
{
    static bool V(AbstractValidator<Alvo> v, string s) { return v.Validate(new Alvo { Nome = s }).IsValid; }

    static void Main()
    {
        Console.WriteLine("CSOUT:min_ok=" + V(new ValMin(), "abc"));
        Console.WriteLine("CSOUT:min_curto=" + V(new ValMin(), "ab"));
        Console.WriteLine("CSOUT:min_null=" + V(new ValMin(), null));
        Console.WriteLine("CSOUT:max_ok=" + V(new ValMax(), "abcde"));
        Console.WriteLine("CSOUT:max_longo=" + V(new ValMax(), "abcdef"));
        Console.WriteLine("CSOUT:exato_ok=" + V(new ValExato(), "abcd"));
        Console.WriteLine("CSOUT:exato_curto=" + V(new ValExato(), "abc"));
        Console.WriteLine("CSOUT:faixa_min=" + V(new ValFaixa(), "ab"));
        Console.WriteLine("CSOUT:faixa_max=" + V(new ValFaixa(), "abcd"));
        Console.WriteLine("CSOUT:faixa_fora=" + V(new ValFaixa(), "abcde"));
    }
}
