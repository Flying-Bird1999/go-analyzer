package service

var MerchantSettingService = &merchantSettingService{}

type merchantSettingService struct{}

type Response struct{}

func WebApiForwardGray() string {
	return "ok"
}

func Fetch[T any]() T {
	var zero T
	return zero
}

func (m *merchantSettingService) UpdateSubMerchantSettingByCode() {}
