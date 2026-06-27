package service

import jsonx "gopkg.inshopline.com/sc1/commons/utils/jsonx"

func CheckIn(v any) string {
	return jsonx.String(v)
}
