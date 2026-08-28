package domain

import "time"

type Establishment struct {
	ID            int64
	Name          string
	AcceptsOrders bool
	APIKeyHash    []byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
