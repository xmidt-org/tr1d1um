package main

type GetSync map[string][]string

func (gs *GetSync) GetList(syncName string) (syncList []string, exists bool) {
	if gs == nil || syncName == "" {
		return nil, false
	}
	syncList, exists = (*gs)[syncName]
	return
}
