package jobs

import (
	"example.com/grpcservice/jobx"
	"example.com/grpcservice/service"
)

func Refresh() { _ = service.BuildReply() }
func Other()   {}

func RegisterTasks(tasks map[string]jobx.TaskFunc) {
	tasks["refresh-reply"] = Refresh
	tasks["other"] = Other
}

func Start() { RegisterTasks(map[string]jobx.TaskFunc{}) }
