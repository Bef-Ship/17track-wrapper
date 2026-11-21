package track17

import "testing"

func TestListCarriers(t *testing.T) {
	list, err := ListCarriers()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 1000 {
		t.Fatalf("expected full 17TRACK catalog, got %d", len(list))
	}
	var ups *Carrier
	for i := range list {
		if list[i].Key == 100002 {
			ups = &list[i]
			break
		}
	}
	if ups == nil || ups.Name == "" {
		t.Fatalf("UPS (100002) missing: %+v", ups)
	}
}

func TestListCarriersWithMock(t *testing.T) {
	base, err := ListCarriers()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ListCarriersWithMock(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != len(base) {
		t.Fatalf("without mock: got %d want %d", len(plain), len(base))
	}
	withNop, err := ListCarriersWithMock(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(withNop) != len(base)+1 {
		t.Fatalf("with mock: got %d want %d", len(withNop), len(base)+1)
	}
	last := withNop[len(withNop)-1]
	if last.Key != NopCarrierKey || last.Name == "" {
		t.Fatalf("nop carrier: %+v", last)
	}
	if base[len(base)-1].Key == NopCarrierKey {
		t.Fatal("ListCarriers must not mutate the cached catalog with nop")
	}
}
