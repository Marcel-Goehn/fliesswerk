# Installs kraftwerk for the current user on Windows:
#
#   irm https://raw.githubusercontent.com/Marcel-Goehn/kraftwerk/main/install.ps1 | iex
#
# $env:KRAFTWERK_VERSION        a release to install instead of the latest, like v1.2.3
# $env:KRAFTWERK_DOWNLOAD_URL   a mirror of the GitHub releases URL
# $env:KRAFTWERK_NO_MODIFY_PATH set to leave your user PATH alone
#
# Runs on Windows PowerShell 5.1 and PowerShell 7. A download made here carries
# no mark of the web, so SmartScreen has nothing to warn about. See
# docs/install.md.

# A script block, so the settings stay out of the caller's session. Errors are
# thrown, not `exit`ed: under `iex`, `exit` would close the user's window.
& {
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue'
	# Windows PowerShell 5.1 may not offer TLS 1.2 by default, and GitHub needs it.
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

	function Say($message) { Write-Output "kraftwerk install: $message" }

	# The binary is unsigned, and Smart App Control only runs signed programs
	# or ones Microsoft knows. 0 is off, 1 on, 2 evaluating.
	$policy = Get-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' -Name 'VerifiedAndReputablePolicyState' -ErrorAction SilentlyContinue
	if ($policy) {
		$blocked = "kraftwerk.exe can't run under Smart App Control until it's signed, and it isn't signed yet."
		if ($policy.VerifiedAndReputablePolicyState -eq 1) {
			throw "kraftwerk install: Smart App Control is on. $blocked Nothing was installed. See https://github.com/Marcel-Goehn/kraftwerk/blob/main/docs/install.md."
		}
		if ($policy.VerifiedAndReputablePolicyState -eq 2) {
			Say "warning: Smart App Control is evaluating this PC. If Windows turns it on: $blocked"
		}
	}

	# A 32-bit or emulated shell sees its own architecture in the first
	# variable and the machine's in the second.
	$machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
	$arch = switch ($machine) {
		'AMD64' { 'amd64' }
		'ARM64' { 'arm64' }
		default { throw "kraftwerk install: no release for this processor ($machine); see https://github.com/Marcel-Goehn/kraftwerk/blob/main/docs/install.md to build from source." }
	}
	$archive = "kraftwerk_windows_$arch.zip"

	$base = if ($env:KRAFTWERK_DOWNLOAD_URL) { $env:KRAFTWERK_DOWNLOAD_URL } else { 'https://github.com/Marcel-Goehn/kraftwerk/releases' }
	$url = if ($env:KRAFTWERK_VERSION) { "$base/download/v$($env:KRAFTWERK_VERSION -replace '^v', '')" } else { "$base/latest/download" }
	$dir = Join-Path (Join-Path $env:LOCALAPPDATA 'Programs') 'kraftwerk'

	$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
	New-Item -ItemType Directory -Path $tmp | Out-Null
	try {
		Say "Downloading $archive"
		$zip = Join-Path $tmp $archive
		$sums = Join-Path $tmp 'checksums.txt'
		Invoke-WebRequest -UseBasicParsing -Uri "$url/$archive" -OutFile $zip
		Invoke-WebRequest -UseBasicParsing -Uri "$url/checksums.txt" -OutFile $sums

		$want = $null
		foreach ($line in Get-Content -LiteralPath $sums) {
			$fields = -split $line
			if ($fields.Count -eq 2 -and $fields[1] -ceq $archive) { $want = $fields[0] }
		}
		if (-not $want) { throw "kraftwerk install: checksums.txt doesn't list $archive, so it can't be verified; nothing was installed." }
		if ((Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash -ine $want) {
			throw "kraftwerk install: the checksum of $archive doesn't match checksums.txt, so the download is damaged or was tampered with; nothing was installed."
		}

		Expand-Archive -LiteralPath $zip -DestinationPath $tmp
		New-Item -ItemType Directory -Force -Path $dir | Out-Null

		# A running program can't be overwritten or deleted, but it can be renamed.
		function Clear-Target($path) {
			if (-not (Test-Path -LiteralPath $path)) { return }
			Remove-Item -LiteralPath "$path.old" -Force -ErrorAction SilentlyContinue
			Move-Item -LiteralPath $path -Destination "$path.old" -Force
			Remove-Item -LiteralPath "$path.old" -Force -ErrorAction SilentlyContinue
		}

		$exe = Join-Path $dir 'kraftwerk.exe'
		Clear-Target $exe
		Move-Item -LiteralPath (Join-Path $tmp 'kraftwerk.exe') -Destination $exe
		Say "Installed kraftwerk to $exe"

		# A kwk.exe in this directory is an earlier install's; a kwk anywhere
		# else is someone else's and keeps the name.
		$kwk = Join-Path $dir 'kwk.exe'
		$taken = @(Get-Command kwk -CommandType Application, ExternalScript -All -ErrorAction SilentlyContinue | Where-Object { (Split-Path -Parent $_.Path) -ine $dir })
		if ((-not (Test-Path -LiteralPath $kwk)) -and $taken.Count -gt 0) {
			Say "Not adding kwk: $($taken[0].Path) already has that name. Use kraftwerk instead."
		} else {
			Clear-Target $kwk
			try {
				New-Item -ItemType HardLink -Path $kwk -Target $exe | Out-Null
			} catch {
				Copy-Item -LiteralPath $exe -Destination $kwk # file systems with no hard links
			}
			Say 'Added kwk as a short name for kraftwerk'
		}

		if ($env:KRAFTWERK_NO_MODIFY_PATH) {
			Say "Add $dir to your PATH to run kraftwerk by name"
		} else {
			# Read and written raw, so entries like %USERPROFILE%\bin stay as they are.
			$userPath = (Get-Item -Path 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames')
			if (@($userPath -split ';' | ForEach-Object { $_.TrimEnd('\') }) -inotcontains $dir) {
				$newPath = if ($userPath) { "$($userPath.TrimEnd(';'));$dir" } else { $dir }
				New-ItemProperty -Path 'HKCU:\Environment' -Name 'Path' -Value $newPath -PropertyType ExpandString -Force | Out-Null
				# Setting any variable this way tells running programs to reread them.
				[Environment]::SetEnvironmentVariable('KRAFTWERK_INSTALL', '1', 'User')
				[Environment]::SetEnvironmentVariable('KRAFTWERK_INSTALL', $null, 'User')
				$env:Path = "$env:Path;$dir"
				Say "Added $dir to your PATH. Open a new terminal to run kraftwerk by name."
			}
		}
	} finally {
		Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
	}
}
