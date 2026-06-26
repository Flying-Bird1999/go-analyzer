package service

var MerchantSettingService = &merchantSettingService{}

type merchantSettingService struct{}

func WebApiForwardGray() string {
	return "ok"
}

func (m *merchantSettingService) UpdateSubMerchantSettingByCode() {}
