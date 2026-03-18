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
    # Install shell completions
    bash_output = Utils.safe_popen_read(bin/"proxy-router", "completion", "bash")
    (etc/"bash_completion.d/proxy-router").write bash_output
    zsh_output = Utils.safe_popen_read(bin/"proxy-router", "completion", "zsh")
    (share/"zsh/site-functions/_proxy-router").write zsh_output
    fish_output = Utils.safe_popen_read(bin/"proxy-router", "completion", "fish")
    (share/"fish/vendor_completions.d/proxy-router.fish").write fish_output
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
    EOS
  end

  test do
    system bin/"proxy-router", "version"
  end
end
