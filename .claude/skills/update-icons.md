# Update Icons

Add or replace tray icons for go-dictate.

## Icon Pipeline

This app uses SVG -> PNG -> runtime ICO generation. Source SVGs live in `icons/` alongside their rendered PNGs. At runtime, `internal/tray/icons.go` loads the PNGs, scales them to multiple sizes, tints them by app state, and builds multi-size ICO data for the Windows system tray.

## Step-by-step

### 1. Prepare the SVG

- Source: [Microsoft Fluent UI System Icons](https://github.com/microsoft/fluentui-system-icons) or user-provided SVGs
- The SVG viewBox can be any size (24, 32, etc.) - we render at high density
- Set `width="128" height="128"` on the `<svg>` element
- Set all `fill` attributes to `#FFFFFF` (white) - the app tints at runtime
- Save to `icons/<name>.svg`

### 2. Convert SVG to PNG

Requires ImageMagick (`magick` command). Render at high density then resize down for crisp edges:

```bash
cd icons
magick -background none -density 384 <name>.svg -resize 128x128 <name>.png
```

The `-density 384` rasterizes at 4x the viewBox size, then `-resize 128x128` downscales with anti-aliasing for a clean result.

### 3. Icon file mapping

The app uses these PNG filenames (hardcoded in `internal/tray/icons.go` `getIconData()`):

| File | App State | Tint Color | Notes |
|------|-----------|------------|-------|
| `mic-on.png` | Speaking (active) | Green `#10B981` | Also used for standby (black/white) and media (amber) |
| `mic-off.png` | Paused/off | Red `#EF4444` | Different shape to visually distinguish paused state |

Currently `mic-on.png` is used for 3 states (standby, speaking, media) with different tint colors. Only `mic-off.png` is a separate shape.

### 4. How runtime tinting works

`icons.go` loads the white-on-transparent PNG, scales it to 5 sizes (16, 20, 24, 32, 48px) using CatmullRom interpolation, then for every pixel with alpha > 0, replaces the RGB with the tint color while preserving the original alpha. This means:

- Source PNGs MUST be white (`#FFFFFF`) on transparent background
- Anti-aliased edges work correctly because alpha is preserved
- Any SVG shape works as long as it renders to white-on-transparent

### 5. Theme-aware standby icon

The standby state detects the Windows taskbar theme via registry key `HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize\SystemUsesLightTheme`:

- Light taskbar -> dark icon `#202020`
- Dark taskbar -> light icon `#D0D0D0`

Other states (green, red, amber) are vivid enough for both themes.

### 6. Adding a new icon

To add a completely new icon file:

1. Create the SVG with white fill, save to `icons/`
2. Convert to PNG: `magick -background none -density 384 new-icon.svg -resize 128x128 new-icon.png`
3. Edit `internal/tray/icons.go` `getIconData()` - add/change the `iconFile` assignment in the relevant `case` branch
4. No other code changes needed - the ICO building, scaling, and tinting are automatic

### 7. Replacing an existing icon

1. Replace the SVG in `icons/` (keep white fill)
2. Re-render the PNG: `magick -background none -density 384 <name>.svg -resize 128x128 <name>.png`
3. Rebuild the app - no code changes needed since the filename hasn't changed

## Icon architecture reference

```
icons/
  mic-on.svg        # Source SVG (white, 128x128)
  mic-on.png        # Rendered PNG (128x128, white on transparent)
  mic-off.svg       # Source SVG (white, 128x128)
  mic-off.png       # Rendered PNG (128x128, white on transparent)

internal/tray/icons.go
  getIconData(colorName)     # Maps app state -> PNG file + tint color
  isLightTaskbar()           # Windows registry theme detection
  loadAndBuildICO(path, c)   # PNG -> scale to 5 sizes -> tint -> multi-size ICO
  buildMultiSizeICO(images)  # ICO header + directory + BMP data per size
  imageToBMP(img)            # RGBA -> ICO-compatible BMP (BGRA, bottom-up, AND mask)
```
