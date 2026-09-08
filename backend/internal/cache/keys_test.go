package cache

import "testing"

func TestChainKeyIncludesChainID(t *testing.T) {
	got := ChainKey("vault", 42161, "position", "0xabc")
	if want := "pb:vault:42161:position:0xabc"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestChainKeyWithoutID(t *testing.T) {
	got := ChainKey("vault", 42161, "tvl", "")
	if want := "pb:vault:42161:tvl"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestOffChainKeyOmitsChainID(t *testing.T) {
	got := Key("auth", "apikey", "k1")
	if want := "pb:auth:apikey:k1"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
