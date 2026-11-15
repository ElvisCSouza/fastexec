package ab

import "testing"

func TestOffWhenDisabled(t *testing.T) {
	c := Config{Enabled: false, Rules: []string{"clean"}}
	if c.Off("clean", "conv:1") {
		t.Fatal("A/B desligado nunca desliga a regra")
	}
}

func TestOffWhenRuleNotCovered(t *testing.T) {
	c := Config{Enabled: true, Rules: []string{"clean"}}
	if c.Off("cut", "conv:1") {
		t.Fatal("regra fora da lista fica ligada")
	}
}

func TestOffWithoutConversation(t *testing.T) {
	c := Config{Enabled: true, Rules: []string{"clean"}}
	if c.Off("clean", "") {
		t.Fatal("sem conversa a regra fica ligada")
	}
}

func TestDeterministic(t *testing.T) {
	c := Config{Enabled: true, Rules: []string{"clean"}, Salt: "s", Percent: 50}
	a := c.Arm("clean", "conv:abc")
	for i := 0; i < 100; i++ {
		if c.Arm("clean", "conv:abc") != a {
			t.Fatal("mesma conversa deve cair sempre no mesmo braço")
		}
	}
}

func TestProportion(t *testing.T) {
	c := Config{Enabled: true, Rules: []string{"clean"}, Salt: "s", Percent: 50}
	treated := 0
	n := 10000
	for i := 0; i < n; i++ {
		if c.Arm("clean", "conv:"+string(rune(i))) == Treated {
			treated++
		}
	}
	if treated < n*45/100 || treated > n*55/100 {
		t.Fatalf("proporção fora de 45-55%%: %d de %d", treated, n)
	}
}

func TestTreatedArmZero(t *testing.T) {
	c := Config{Enabled: true, Rules: []string{"clean"}, Salt: "s", Percent: 50, TreatedArm: "0"}
	for i := 0; i < 200; i++ {
		conv := "conv:" + string(rune(i))
		if c.Arm("clean", conv) == 0 && c.Off("clean", conv) {
			t.Fatal("com treated_arm=0 o braço 0 mantém a regra ligada")
		}
		if c.Arm("clean", conv) == 1 && !c.Off("clean", conv) {
			t.Fatal("com treated_arm=0 o braço 1 desliga a regra")
		}
	}
}
