package domain

import "time"

type Category struct {
	ID              int64
	EstablishmentID int64
	Name            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Dish struct {
	ID          int64
	CategoryID  int64
	Name        string
	Description string
	PriceMinor  int64
	IsAvailable bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Menu struct {
	Establishment Establishment
	Categories    []MenuCategory
}

type MenuCategory struct {
	Category Category
	Dishes   []Dish
}
