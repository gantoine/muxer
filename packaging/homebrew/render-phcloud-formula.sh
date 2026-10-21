#!/bin/bash
set -euo pipefail

if [ $# -lt 3 ]; then
  echo "usage: $0 VERSION ARM64_SHA256 AMD64_SHA256 [DOWNLOAD_BASE_URL]" >&2
  exit 2
fi

version=$1
arm64_sha=$2
amd64_sha=$3
base_url=${4:-https://github.com/gantoine/muxer/releases/download/$version}

cat <<FORMULA
class Phcloud < Formula
  desc "Lists, starts and follows PostHog Cloud runs from the cmux Dock"
  homepage "https://github.com/gantoine/muxer/tree/main/phcloud"
  version "$version"
  license "MIT"

  if Hardware::CPU.arm?
    url "$base_url/phcloud-$version-darwin-arm64.tar.gz"
    sha256 "$arm64_sha"
  else
    url "$base_url/phcloud-$version-darwin-amd64.tar.gz"
    sha256 "$amd64_sha"
  end

  depends_on :macos

  def install
    bin.install "phcloud"
  end

  def caveats
    <<~EOS
      Connect PostHog and pin the run manager in the cmux Dock:
        phcloud connect
        phcloud dock install
    EOS
  end

  test do
    assert_equal "phcloud #{version}", shell_output("#{bin}/phcloud version").strip
  end
end
FORMULA
