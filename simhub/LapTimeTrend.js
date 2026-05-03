// LapTimeTrend.js — SimHub Computed Property
// Compares the most recently completed lap time to the one before it.
//
// Exposes:
//   ComputedPropertiesPlugin.LapTimeTrendColor
//     YellowGreen = faster than previous lap
//     Tomato      = slower than previous lap
//     DimGray     = within threshold (about the same)

const LAP_TIME_SOURCE = 'DataCorePlugin.GameData.LastLapTime';
const COLOR_PROP      = 'ComputedPropertiesPlugin.LapTimeTrendColor';

// --- Configurable ---
const THRESHOLD     = 0.1;           // seconds of difference to leave DimGray
const COLOR_FASTER  = 'YellowGreen';
const COLOR_SLOWER  = 'Tomato';
const COLOR_NEUTRAL = 'DimGray';
// --------------------

var prevLapTime = null;

function init() {
    createProperty(COLOR_PROP);
    setPropertyValue(COLOR_PROP, COLOR_NEUTRAL);
    subscribe(LAP_TIME_SOURCE, 'onLastLapTimeChanged');
}

function onLastLapTimeChanged() {
    var raw = getPropertyValue(LAP_TIME_SOURCE);
    if (raw === null) return;

    // LastLapTime is a .NET TimeSpan — extract seconds as a number
    var current = raw.TotalSeconds !== undefined ? raw.TotalSeconds : Number(raw);
    if (isNaN(current) || current <= 0) return;

    if (prevLapTime !== null) {
        var delta = current - prevLapTime;   // negative = faster, positive = slower
        setPropertyValue(COLOR_PROP,
            delta < -THRESHOLD ? COLOR_FASTER :
            delta >  THRESHOLD ? COLOR_SLOWER : COLOR_NEUTRAL);
    }

    prevLapTime = current;
}
