//go:build !linux

package oreate

import "github.com/chromedp/chromedp"

func browserIsolationOptions() []chromedp.ExecAllocatorOption { return nil }
