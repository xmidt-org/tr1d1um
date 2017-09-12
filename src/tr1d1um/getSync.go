package main

import (
    "net/http"
    
    HTH "github.comcast.com/webpa/health"
    TS "github.comcast.com/webpa/tscommon"
)

type GetSync map[string][]string

func (gs *GetSync) GetList(syncName string) (syncList []string, exists bool) {
	if gs == nil || syncName == "" {
		return nil, false
	}
	syncList, exists = (*gs)[syncName]
	return
}

func getSyncHandle(rw http.ResponseWriter, req *http.Request) {
	//log.Debug("getSyncHandle called")
	//health.SendEvent(HTH.Inc(TotalRESTMessagesServiced, 1))

	//log.Trace("tConfig.GetSyncList %#v", tConfig.GetSyncList)
	if tConfig.GetSyncList != nil {
		TS.ResponseJson(tConfig.GetSyncList, rw)
	} else {
		TS.ResponseJson(GetSync{}, rw)
	}
}
