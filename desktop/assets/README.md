# FluxGet application icon

`fluxget.png` is the original high-resolution PNG with transparent corners. It is embedded in the desktop executable by `../icon.go`, so the window icon does not depend on a runtime asset path. The Linux launcher template is `../../packaging/linux/io.github.parsania80.fluxget.desktop`; its icon name matches the application ID.

For Linux installation, create square PNG variants at 16, 24, 32, 48, 64, 128, 256, 512, and 1024 pixels from this source, preserving alpha, and install them as `io.github.parsania80.fluxget.png` under the corresponding `~/.local/share/icons/hicolor/<size>x<size>/apps/` directories. The installed desktop entry uses the actual executable path. The original generated image is retained in this repository.

## Generation

Created with the built-in imagegen tool on 2026-11-09. Prompt:

> Use case: logo-brand. Asset type: production Linux desktop application icon for FluxGet, a reliable download manager with an existing dark charcoal and teal interface. Create one polished original app icon: a bold sculptural downward download arrow whose upper stem suggests fast flowing parallel streams, centered on a soft rounded-square deep charcoal tile. Teal-to-cyan color accents drawn from the existing interface, restrained dimensional depth and crisp beveled surfaces, strong simple silhouette readable at 32px, balanced generous internal padding. Face-on square composition, approximately 85% canvas occupancy, no perspective tilt. The tile is the icon itself; outside its rounded corners must be genuinely transparent. No text, letters, words, watermarks, extra objects, decorative background, mockup, or multiple variants. High-resolution square PNG with clean alpha edges, suitable for launcher, dock, and window icon.
