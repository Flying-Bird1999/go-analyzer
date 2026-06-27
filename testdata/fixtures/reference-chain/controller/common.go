package controller

import (
	missing "example.com/reference-chain/missing"
	svc "example.com/reference-chain/service"
)

func CheckIn() string {
	_ = missing.Request{}
	missing.Load()
	_ = svc.Fetch[svc.Response]()
	return svc.WebApiForwardGray()
}

func Update() {
	svc.MerchantSettingService.UpdateSubMerchantSettingByCode()
}
