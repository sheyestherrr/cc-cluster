package cli

import (
	"context"
	"errors"
)

// cmdWeb 启动网页界面。
func (a *app) cmdWeb(ctx context.Context, args []string) error {
	return errors.New("此版本未包含网页界面")
}
