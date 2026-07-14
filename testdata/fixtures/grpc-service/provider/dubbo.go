package provider

import (
	"example.com/grpcservice/dubbox"
	"example.com/grpcservice/service"
)

var replyAPI = &ReplyAPI{}

type ReplyAPI struct{}

func ExportReplyAPI() {
	dubbox.GetRootConfig().Provider.Services["ReplyAPI"] = &dubbox.ServiceConfig{
		Interface: "example.reply.ReplyAPI",
		Version:   "1.0.0",
		Methods: []*dubbox.MethodConfig{
			{Name: "reply", Retries: "0"},
			{Name: "other", Retries: "0"},
		},
	}
	dubbox.SetProviderService(replyAPI)
}

func (r *ReplyAPI) MethodMapper() map[string]string {
	return map[string]string{
		"Reply": "reply",
		"Other": "other",
	}
}

func (r *ReplyAPI) Reply() string { return service.BuildReply() }

func (r *ReplyAPI) Other() string { return "other" }
