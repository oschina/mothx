#!/bin/bash
set -e

# MothX Debian Package Builder
# Usage: ./scripts/build-deb.sh <arch> [version]
# Example: ./scripts/build-deb.sh amd64 v0.0.2

BINARY_NAME="mothx"
PACKAGE_NAME="mothx"
MAINTAINER="MothX Team <admin@pkold.com>"
DESCRIPTION="AI-powered terminal coding assistant"
HOMEPAGE="https://github.com/oschina/mothx"

# Parse arguments
ARCH="${1:-amd64}"
VERSION="${2:-$(git describe --tags --always 2>/dev/null || echo "0.0.1")}"

# Remove leading 'v' if present
VERSION="${VERSION#v}"
VERSION="${VERSION%-dirty}"

BUILD_DIR="dist/deb"
PACKAGE_DIR="${BUILD_DIR}/${PACKAGE_NAME}_${VERSION}_${ARCH}"

# Determine if this is a musl build
IS_MUSL=false
if echo "${ARCH}" | grep -q "musl"; then
    IS_MUSL=true
    # Extract base arch: "amd64-musl" -> "amd64", "musl-amd64" -> "amd64"
    BASE_ARCH=$(echo "${ARCH}" | sed 's/-musl//;s/musl-//')
    BINARY_FILE="bin/${BINARY_NAME}-linux-musl-${BASE_ARCH}"
    # Normalize arch for package name: use amd64-musl / arm64-musl (hyphens only, no underscores)
    DEB_ARCH="${BASE_ARCH}-musl"
    PACKAGE_DIR="${BUILD_DIR}/${PACKAGE_NAME}_${VERSION}_${DEB_ARCH}"
else
    BINARY_FILE="bin/${BINARY_NAME}-linux-${ARCH}"
    DEB_ARCH="${ARCH}"
fi

echo "Building ${PACKAGE_NAME} ${VERSION} for ${DEB_ARCH}..."

# Check if binary exists
if [ ! -f "${BINARY_FILE}" ]; then
    echo "Error: Binary not found: ${BINARY_FILE}"
    echo "Run 'make build-linux' or 'make build-linux-musl' first"
    exit 1
fi

# Clean previous build
rm -rf "${PACKAGE_DIR}"
mkdir -p "${PACKAGE_DIR}/DEBIAN"
mkdir -p "${PACKAGE_DIR}/usr/bin"
mkdir -p "${PACKAGE_DIR}/usr/share/doc/${PACKAGE_NAME}"
mkdir -p "${PACKAGE_DIR}/usr/share/licenses/${PACKAGE_NAME}"

# Copy binary
cp "${BINARY_FILE}" "${PACKAGE_DIR}/usr/bin/${BINARY_NAME}"
chmod +x "${PACKAGE_DIR}/usr/bin/${BINARY_NAME}"

# Create control file
if [ "${IS_MUSL}" = true ]; then
    cat > "${PACKAGE_DIR}/DEBIAN/control" << EOF
Package: ${PACKAGE_NAME}-musl
Version: ${VERSION}
Section: devel
Priority: optional
Architecture: ${DEB_ARCH}
Maintainer: ${MAINTAINER}
Homepage: ${HOMEPAGE}
Description: ${DESCRIPTION} (musl static build)
 MothX is a terminal-based AI coding assistant that supports
 multiple LLM providers, sandboxed execution, and a rich TUI.
 This is a statically linked musl build for musl-based distributions
 (e.g., Alpine Linux, Void Linux musl).
EOF
else
    cat > "${PACKAGE_DIR}/DEBIAN/control" << EOF
Package: ${PACKAGE_NAME}
Version: ${VERSION}
Section: devel
Priority: optional
Architecture: ${DEB_ARCH}
Depends: libc6
Maintainer: ${MAINTAINER}
Homepage: ${HOMEPAGE}
Description: ${DESCRIPTION}
 MothX is a terminal-based AI coding assistant that supports
 multiple LLM providers, sandboxed execution, and a rich TUI.
 It helps developers write, edit, and understand code using AI.
EOF
fi

# Create copyright file
cat > "${PACKAGE_DIR}/usr/share/licenses/${PACKAGE_NAME}/copyright" << EOF
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: ${PACKAGE_NAME}
Upstream-Contact: ${MAINTAINER}
Source: ${HOMEPAGE}

Files: *
Copyright: 2024 MothX Team
License: MIT
 Permission is hereby granted, free of charge, to any person obtaining a
 copy of this software and associated documentation files (the "Software"),
 to deal in the Software without restriction, including without limitation
 the rights to use, copy, modify, merge, publish, distribute, sublicense,
 and/or sell copies of the Software, and to permit persons to whom the
 Software is furnished to do so, subject to the following conditions:
 .
 The above copyright notice and this permission notice shall be included
 in all copies or substantial portions of the Software.
 .
 THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
 THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
 FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
 DEALINGS IN THE SOFTWARE.
EOF

# Create changelog
cat > "${PACKAGE_DIR}/usr/share/doc/${PACKAGE_NAME}/changelog.Debian" << EOF
${PACKAGE_NAME} (${VERSION}) unstable; urgency=low

  * Release ${VERSION}

 -- ${MAINTAINER}  $(date -R)
EOF

# Compress changelog
gzip -9 -n "${PACKAGE_DIR}/usr/share/doc/${PACKAGE_NAME}/changelog.Debian"

# Create postinst script
cat > "${PACKAGE_DIR}/DEBIAN/postinst" << 'EOF'
#!/bin/bash
set -e

echo "MothX installed successfully!"
echo "Run 'mothx' to get started."
EOF
chmod 755 "${PACKAGE_DIR}/DEBIAN/postinst"

# Create postrm script
cat > "${PACKAGE_DIR}/DEBIAN/postrm" << 'EOF'
#!/bin/bash
set -e

# Clean up any temporary files if needed
true
EOF
chmod 755 "${PACKAGE_DIR}/DEBIAN/postrm"

# Build the package
echo "Building .deb package..."
dpkg-deb --root-owner-group --build "${PACKAGE_DIR}"

# Generate checksums
cd "${BUILD_DIR}"
DEB_FILE="${PACKAGE_NAME}_${VERSION}_${DEB_ARCH}.deb"
sha256sum "${DEB_FILE}" > "${DEB_FILE}.sha256"
cd - > /dev/null

# Cleanup temp directory
rm -rf "${PACKAGE_DIR}"

echo "  Created: ${BUILD_DIR}/${DEB_FILE}"
