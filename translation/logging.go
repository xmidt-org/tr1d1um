package translation

import "log"

type loggingService struct {
	logger log.Logger
	Service
}
