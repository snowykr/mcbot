package backup

import "errors"

// ErrRetentionAfterCreate indicates the backup archive was created successfully
// but post-create retention pruning failed.
var ErrRetentionAfterCreate = errors.New("retention after backup failed")
