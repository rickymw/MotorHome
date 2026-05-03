// GapTrend.js — SimHub Computed Property
// Samples the gap to the car ahead/behind at each S/F crossing and compares
// it to the previous lap. More stable than per-sample comparison because it
// ignores intra-lap variation (relative speed through corners, straights, etc.)
//
// Exposes:
//   ComputedPropertiesPlugin.GapAheadColor
//   ComputedPropertiesPlugin.GapBehindColor
//     YellowGreen = gaining  |  Tomato = losing  |  DimGray = about equal
//
// Verify source property names in SimHub → Available Properties while iRacing
// is running. The distance unit (metres or seconds) sets the right THRESHOLD.

const GAP_AHEAD_SOURCE  = 'IRacingExtraProperties.iRacing_DriverAhead_00_Distance';
const GAP_BEHIND_SOURCE = 'IRacingExtraProperties.iRacing_DriverBehind_00_Distance';
const LAP_SOURCE        = 'DataCorePlugin.GameData.CompletedLaps';

const COLOR_AHEAD_PROP  = 'ComputedPropertiesPlugin.GapAheadColor';
const COLOR_BEHIND_PROP = 'ComputedPropertiesPlugin.GapBehindColor';

// --- Configurable ---
const THRESHOLD     = 5;             // change needed to leave DimGray (metres or seconds)
const COLOR_GAINING = 'YellowGreen';
const COLOR_LOSING  = 'Tomato';
const COLOR_NEUTRAL = 'DimGray';
// --------------------

var prevLap       = null;
var prevGapAhead  = null;
var prevGapBehind = null;

function init() {
    createProperty(COLOR_AHEAD_PROP);
    createProperty(COLOR_BEHIND_PROP);
    setPropertyValue(COLOR_AHEAD_PROP,  COLOR_NEUTRAL);
    setPropertyValue(COLOR_BEHIND_PROP, COLOR_NEUTRAL);
    subscribe(LAP_SOURCE, 'onLapChanged');
}

function onLapChanged() {
    var lap = getPropertyValue(LAP_SOURCE);
    if (lap === null) return;

    var gapAhead  = getPropertyValue(GAP_AHEAD_SOURCE);
    var gapBehind = getPropertyValue(GAP_BEHIND_SOURCE);

    if (prevLap !== null && lap !== prevLap) {
        // S/F line crossed — compare to previous lap's snapshot
        if (prevGapAhead !== null && gapAhead !== null) {
            var dAhead = gapAhead - prevGapAhead;
            // gap_ahead shrinking = closing on car ahead = gaining
            setPropertyValue(COLOR_AHEAD_PROP,
                dAhead < -THRESHOLD ? COLOR_GAINING :
                dAhead >  THRESHOLD ? COLOR_LOSING  : COLOR_NEUTRAL);
        }
        if (prevGapBehind !== null && gapBehind !== null) {
            var dBehind = gapBehind - prevGapBehind;
            // gap_behind growing = pulling away from car behind = gaining
            setPropertyValue(COLOR_BEHIND_PROP,
                dBehind >  THRESHOLD ? COLOR_GAINING :
                dBehind < -THRESHOLD ? COLOR_LOSING  : COLOR_NEUTRAL);
        }
    }

    // Store this crossing as the reference for the next lap
    prevLap       = lap;
    prevGapAhead  = gapAhead;
    prevGapBehind = gapBehind;
}
