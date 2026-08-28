package handlers

import (
	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
	"kitchen-api/internal/kitchen/service"
)

func establishmentToAPI(establishment domain.Establishment) api.Establishment {
	return api.Establishment{
		ID:            establishment.ID,
		Name:          establishment.Name,
		AcceptsOrders: establishment.AcceptsOrders,
	}
}

func establishmentsToAPI(establishments []domain.Establishment) []api.Establishment {
	result := make([]api.Establishment, len(establishments))
	for index, establishment := range establishments {
		result[index] = establishmentToAPI(establishment)
	}

	return result
}

func categoryToAPI(category domain.Category) api.Category {
	return api.Category{ID: category.ID, Name: category.Name}
}

func dishToAPI(dish domain.Dish) api.Dish {
	return api.Dish{
		ID:          dish.ID,
		CategoryID:  dish.CategoryID,
		Name:        dish.Name,
		Description: dish.Description,
		PriceMinor:  dish.PriceMinor,
		IsAvailable: dish.IsAvailable,
	}
}

func menuToAPI(menu domain.Menu) api.MenuResponse {
	categories := make([]api.MenuCategory, len(menu.Categories))
	for categoryIndex, category := range menu.Categories {
		dishes := make([]api.MenuDish, len(category.Dishes))
		for dishIndex, dish := range category.Dishes {
			dishes[dishIndex] = api.MenuDish{
				ID:          dish.ID,
				Name:        dish.Name,
				Description: dish.Description,
				PriceMinor:  dish.PriceMinor,
				IsAvailable: dish.IsAvailable,
			}
		}

		categories[categoryIndex] = api.MenuCategory{
			ID:     category.Category.ID,
			Name:   category.Category.Name,
			Dishes: dishes,
		}
	}

	return api.MenuResponse{
		Establishment: establishmentToAPI(menu.Establishment),
		Categories:    categories,
	}
}

func orderItemsToAPI(items []domain.OrderItem) []api.OrderItem {
	result := make([]api.OrderItem, len(items))
	for index, item := range items {
		result[index] = api.OrderItem{
			DishID:         item.DishID,
			DishName:       item.DishName,
			UnitPriceMinor: item.UnitPriceMinor,
			Quantity:       item.Quantity,
		}
	}

	return result
}

func trackedOrderToAPI(order domain.Order, establishment domain.Establishment) api.TrackedOrder {
	return api.TrackedOrder{
		Status: api.OrderStatus(order.Status),
		Establishment: api.EstablishmentSummary{
			ID:   establishment.ID,
			Name: establishment.Name,
		},
		OrderItems:       orderItemsToAPI(order.Items),
		TotalMinor:       order.TotalMinor,
		RejectionReason:  rejectionReasonToAPI(order.RejectionReason),
		RejectionComment: order.RejectionComment,
		CreatedAt:        order.CreatedAt,
		UpdatedAt:        order.UpdatedAt,
	}
}

func partnerOrderToAPI(order domain.Order) api.PartnerOrder {
	return api.PartnerOrder{
		ID:               order.ID,
		Status:           api.OrderStatus(order.Status),
		CustomerName:     order.CustomerName,
		CustomerPhone:    order.CustomerPhone,
		DeliveryAddress:  order.DeliveryAddress,
		Comment:          order.Comment,
		OrderItems:       orderItemsToAPI(order.Items),
		TotalMinor:       order.TotalMinor,
		RejectionReason:  rejectionReasonToAPI(order.RejectionReason),
		RejectionComment: order.RejectionComment,
		CreatedAt:        order.CreatedAt,
		UpdatedAt:        order.UpdatedAt,
	}
}

func partnerOrdersToAPI(orders []domain.Order) []api.PartnerOrder {
	result := make([]api.PartnerOrder, len(orders))
	for index, order := range orders {
		result[index] = partnerOrderToAPI(order)
	}

	return result
}

func rejectionReasonToAPI(reason *domain.RejectionReason) *api.NullableRejectionReason {
	if reason == nil {
		return nil
	}

	converted := api.NullableRejectionReason(*reason)
	return &converted
}

func dishConflictsToAPI(conflicts []service.DishConflict) []api.DishConflict {
	result := make([]api.DishConflict, len(conflicts))
	for index, conflict := range conflicts {
		result[index] = api.DishConflict{
			DishID:                conflict.DishID,
			Reason:                api.DishConflictReason(conflict.Reason),
			CurrentUnitPriceMinor: conflict.CurrentUnitPriceMinor,
		}
	}

	return result
}
