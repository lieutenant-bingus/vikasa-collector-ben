package synth

import (
	"reflect"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewSiteInventoryDiffer emits a site-inventory-report on first read and
// whenever any inventory field changes.
func NewSiteInventoryDiffer() Differ { return siteInventoryDiffer{} }

type siteInventoryDiffer struct{}

func (siteInventoryDiffer) Kind() model.Kind { return model.KindSiteInventory }

func (siteInventoryDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.SiteInventory)
	if prev != nil && reflect.DeepEqual(prev.(model.SiteInventory), c) {
		return nil
	}
	approaches := append([]model.PhaseApproach(nil), c.PhaseApproaches...)
	return []model.Event{model.SiteInventoryReport{
		Base:            base,
		MainStreet:      c.MainStreet,
		SecondStreet:    c.SecondStreet,
		Description:     c.Description,
		LatitudeE7:      c.LatitudeE7,
		LongitudeE7:     c.LongitudeE7,
		MapHex:          c.MapHex,
		SpatHex:         c.SpatHex,
		MapMsgID:        c.MapMsgID,
		SpatMsgID:       c.SpatMsgID,
		PhaseApproaches: approaches,
	}}
}
