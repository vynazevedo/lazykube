package app

import (
	"fmt"
	guilib "github.com/TNK-Studio/lazykube/pkg/gui"
	"strings"
)

const (
	resizeableViewMinHeight = 5
	clusterInfoViewHeight   = 2
	optionViewHeight        = 1
)

var (
	viewHeights     = map[string]int{}
	functionViews   = []string{clusterInfoViewName, namespaceViewName, serviceViewName, deploymentViewName, podViewName}
	resizeableViews = []string{namespaceViewName, serviceViewName, deploymentViewName, podViewName}

	reactiveHeightCache = map[string]int{}
	migrateTopCache     = map[string]int{}
)

func aboveViewNameFunc(_ *guilib.Gui, view *guilib.View) string {
	for index, viewName := range functionViews {
		if viewName == view.Name && index != 0 {
			return functionViews[index-1]
		}
	}
	return ""
}

func resizeableView(viewName string) bool {
	for _, resizeableView := range resizeableViews {
		if resizeableView == viewName {
			return true
		}
	}
	return false
}

func leftSideWidth(maxWidth int) int {
	return maxWidth / 3
}

func usableSpace(maxHeight int) int {
	usedSpace := clusterInfoViewHeight + optionViewHeight + (len(functionViews) - 1)
	if maxHeight < resizeableViewMinHeight*len(resizeableViews)-usedSpace {
		return maxHeight - 2
	}
	return maxHeight - usedSpace
}

func resizePanelHeight(gui *guilib.Gui) error {
	_, maxHeight := gui.Size()

	space := usableSpace(maxHeight)

	n := len(resizeableViews)
	viewHeights[clusterInfoViewName] = clusterInfoViewHeight
	for _, resizeableView := range resizeableViews {
		viewHeights[resizeableView] = space / n
	}
	viewHeights[optionViewName] = optionViewHeight

	return nil
}

func reactiveHeight(gui *guilib.Gui, view *guilib.View) int {
	var currentViewName string
	currentView := gui.CurrentView()
	if currentView != nil {
		currentViewName = currentView.Name
	}
	previousViewName := gui.PeekPreviousView()

	height := cacheAbleReactiveHeight(gui.MaxHeight(), resizeableViews, currentViewName, previousViewName, view.Name)
	return height
}

func cacheAbleReactiveHeight(maxHeight int, resizeableViews []string, currentViewName, previousViewName, viewName string) int {
	key := fmt.Sprintf("%d,[%s],%s,%s,%s", maxHeight, strings.Join(resizeableViews, ","), currentViewName, previousViewName, viewName)
	cacheVal, ok := reactiveHeightCache[key]
	if ok {
		return cacheVal
	}

	var resizeView string
	if currentViewName == "" {
		resizeView = resizeableViews[0]
	} else {
		resizeView = currentViewName
	}

	if !resizeableView(resizeView) {
		resizeView = previousViewName

		if !resizeableView(resizeView) {
			resizeView = resizeableViews[0]
		}
	}

	n := len(resizeableViews)
	height := viewHeights[viewName]

	smallScreen := maxHeight < heightBoundary() && resizeableView(viewName)
	if !smallScreen && resizeView == viewName {
		height += usableSpace(maxHeight) % n
	}

	cacheVal = height
	reactiveHeightCache[key] = cacheVal
	return cacheVal
}

func migrateTopFunc(gui *guilib.Gui, view *guilib.View) int {
	var currentViewName string
	currentView := gui.CurrentView()
	if currentView != nil {
		currentViewName = currentView.Name
	}
	return cacheAbleMigrateTopFunc(gui.MaxHeight(), resizeableViews, currentViewName, view.Name)
}

func cacheAbleMigrateTopFunc(maxHeight int, resizeableViews []string, currentViewName, viewName string) int {
	key := fmt.Sprintf("%d,%s,%s,%s", maxHeight, strings.Join(resizeableViews, ","), currentViewName, viewName)
	if cacheVal, ok := migrateTopCache[key]; ok {
		return cacheVal
	}

	migrateTopCache[key] = defaultMigrateTop
	return defaultMigrateTop
}

const defaultMigrateTop = 1

func heightBoundary() int {
	usedSpace := clusterInfoViewHeight - optionViewHeight - (len(functionViews) - 1)
	boundary := resizeableViewMinHeight*len(resizeableViews) - usedSpace
	return boundary
}
