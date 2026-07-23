package rtk

import "strconv"

const (
	rawCap             = 10 * 1024 * 1024
	minCompressSize    = 500
	detectWindow       = 1024
	gitDiffHunkMax     = 100
	gitLogMaxLines     = 200
	dedupLineMax       = 2000
	grepPerFileMax     = 10
	findPerDirMax      = 10
	findTotalDirMax    = 20
	statusMaxFiles     = 10
	statusMaxUntrack   = 10
	lsExtSummaryTop    = 5
	treeMaxLines       = 200
	smartTruncHead     = 120
	smartTruncTail     = 60
	smartTruncMinLines = 250
)

func itoa(n int) string { return strconv.Itoa(n) }
