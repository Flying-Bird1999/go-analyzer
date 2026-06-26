package controller

import svc "example.com/reference-chain/service"

func CheckIn() string {
	return svc.WebApiForwardGray()
}

func Update() {
	svc.MerchantSettingService.UpdateSubMerchantSettingByCode()
}
