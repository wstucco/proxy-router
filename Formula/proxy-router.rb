class ProxyRouter < Formula
  desc "Local proxy that routes connections to an upstream or direct based on configurable rules"
  homepage "https://github.com/wstucco/proxy-router"
  version "0.0.0" # updated by CI

  on_arm do
    url "https://github.com/wstucco/proxy-router/releases/download/v#{version}/proxy-router-v#{version}-darwin-arm64.tar.gz"
    sha256 "placeholder" # updated by CI
  end

  def install
    bin.install "proxy-router"

    # Generate shell completions
    (bash_completion/"proxy-router").write Utils.safe_popen_read(bin/"proxy-router", "completion", "bash")
    (zsh_completion/"_proxy-router").write Utils.safe_popen_read(bin/"proxy-router", "completion", "zsh")
    (fish_completion/"proxy-router.fish").write Utils.safe_popen_read(bin/"proxy-router", "completion", "fish")
  end

  service do
    run [opt_bin/"proxy-router", "run", "-config", etc/"proxy-router/config.json"]
    keep_alive true
    log_path var/"log/proxy-router.log"
    error_log_path var/"log/proxy-router.err"
  end

  def post_install
    (etc/"proxy-router").mkpath
    unless (etc/"proxy-router/config.json").exist?
      (etc/"proxy-router/config.json").write Utils.safe_popen_read(bin/"proxy-router", "run", "-gen-config")
    end
  end

  def caveats
    <<~EOS
      To start proxy-router as a service:
        brew services start proxy-router

      Config file: #{etc}/proxy-router/config.json
      Logs:        #{var}/log/proxy-router.{log,err}
    EOS
  end

  test do
    system bin/"proxy-router", "version"
  end
end
