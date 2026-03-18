class ProxyRouter < Formula
  desc "Local proxy that routes connections to an upstream or direct based on configurable rules"
  homepage "https://github.com/wstucco/proxy-router"
  version "0.0.0" # updated by CI

  on_arm do
    url "https://github.com/wstucco/proxy-router/releases/download/v#{version}/proxy-router-v#{version}-darwin-arm64"
    sha256 "placeholder" # updated by CI
  end

  def install
    bin.install "proxy-router-v#{version}-darwin-arm64" => "proxy-router"
    # Install shell completions in Homebrew's global completions directories
    bash_output = Utils.safe_popen_read(bin/"proxy-router", "completion", "bash")
    (buildpath/"proxy-router.bash").write bash_output
    bash_completion.install "proxy-router.bash" => "proxy-router"

    zsh_output = Utils.safe_popen_read(bin/"proxy-router", "completion", "zsh")
    (buildpath/"_proxy-router").write zsh_output
    zsh_completion.install "_proxy-router"

    fish_output = Utils.safe_popen_read(bin/"proxy-router", "completion", "fish")
    (buildpath/"proxy-router.fish").write fish_output
    fish_completion.install "proxy-router.fish"
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
      system bin/"proxy-router", "-gen-config" do |io|
        (etc/"proxy-router/config.json").write io.read
      end
    end
  end

  def caveats
    <<~EOS
      Shell completions for bash, zsh, and fish are installed automatically.

      To start proxy-router as a service:
        brew services start proxy-router

      Config file: #{etc}/proxy-router/config.json
      Logs:        #{var}/log/proxy-router.{log,err}

      When you run:
        brew uninstall --zap proxy-router
      Homebrew will prompt to remove all configuration and log files for proxy-router.
    EOS
  end

  zap do
    delete etc/"proxy-router"
    delete var/"log/proxy-router.log"
    delete var/"log/proxy-router.err"
  end
  end

  test do
    system bin/"proxy-router", "version"
  end
end
