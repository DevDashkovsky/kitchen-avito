package service

import (
	"bytes"
	"testing"
)

func TestFingerprintUsesNormalizedIntent(t *testing.T) {
	base := CreateOrderInput{EstablishmentID: 1, TrackingTokenHash: make([]byte, 32), CustomerName: " Аня ", CustomerPhone: " 12345 ", DeliveryAddress: " Адрес ", Items: []CreateOrderItem{{1, 1, 100}, {2, 1, 200}, {1, 1, 100}}}
	fingerprint := func(in CreateOrderInput) []byte {
		t.Helper()
		normalized, err := normalizeCreateOrderInput(in)
		if err != nil {
			t.Fatal(err)
		}
		normalized.Items, _, err = combineOrderItems(normalized.Items)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := orderFingerprint(normalized)
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	want := fingerprint(base)
	equivalent := base
	equivalent.CustomerName, equivalent.CustomerPhone, equivalent.DeliveryAddress = "Аня", "12345", "Адрес"
	equivalent.Items = []CreateOrderItem{{2, 1, 200}, {1, 2, 100}}
	if !bytes.Equal(want, fingerprint(equivalent)) {
		t.Fatal("equivalent requests have different fingerprints")
	}
	tests := map[string]func(*CreateOrderInput){
		"venue":                           func(v *CreateOrderInput) { v.EstablishmentID++ },
		"name":                            func(v *CreateOrderInput) { v.CustomerName = "Борис" },
		"phone":                           func(v *CreateOrderInput) { v.CustomerPhone = "67890" },
		"address":                         func(v *CreateOrderInput) { v.DeliveryAddress = "Другой адрес" },
		"empty comment differs from null": func(v *CreateOrderInput) { value := ""; v.Comment = &value },
		"quantity":                        func(v *CreateOrderInput) { v.Items = []CreateOrderItem{{1, 1, 100}, {2, 1, 200}} },
		"price":                           func(v *CreateOrderInput) { v.Items = []CreateOrderItem{{1, 2, 101}, {2, 1, 200}} },
		"dish":                            func(v *CreateOrderInput) { v.Items = []CreateOrderItem{{3, 2, 100}, {2, 1, 200}} },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			input := base
			change(&input)
			if bytes.Equal(want, fingerprint(input)) {
				t.Fatal("changed intent retained fingerprint")
			}
		})
	}
	// Credentials and transport mode are not part of the order's intent.
	changedKey := base
	changedKey.TrackingTokenHash = bytes.Repeat([]byte{1}, 32)
	changedKey.Idempotent = true
	if !bytes.Equal(want, fingerprint(changedKey)) {
		t.Fatal("credential influenced intent")
	}
}
