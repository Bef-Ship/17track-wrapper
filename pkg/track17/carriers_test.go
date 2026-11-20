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
