BEGIN {
	if (minimum == "") {
		minimum = 90
	}
	if (output == "") {
		output = "coverage.txt"
	}
}

NR == 1 {
	mode = $0
	next
}

{
	block = $1 SUBSEP $2
	if (!(block in block_seen)) {
		block_seen[block] = 1
		block_order[++block_count] = block
		block_text[block] = $1 " " $2

		split($1, location, ":")
		file = location[1]
		package = file
		sub("/[^/]+$", "", package)

		block_file[block] = file
		block_package[block] = package
		block_statements[block] = $2

		if (!(file in file_seen)) {
			file_seen[file] = 1
			file_order[++file_count] = file
		}
		if (!(package in package_seen)) {
			package_seen[package] = 1
			package_order[++package_count] = package
		}
	}
	if (($3 + 0) > block_hits[block]) {
		block_hits[block] = $3 + 0
	}
}

END {
	print mode > output
	for (i = 1; i <= block_count; i++) {
		block = block_order[i]
		print block_text[block], block_hits[block] + 0 >> output

		file = block_file[block]
		package = block_package[block]
		statements = block_statements[block]
		file_total[file] += statements
		package_total[package] += statements
		repository_total += statements
		if (block_hits[block] > 0) {
			file_covered[file] += statements
			package_covered[package] += statements
			repository_covered += statements
		}
	}
	close(output)

	failed = 0
	printf "Merged coverage (minimum %.2f%% per package):\n", minimum
	for (i = 1; i <= package_count; i++) {
		package = package_order[i]
		if (package_total[package] == 0) {
			printf "  %-68s %6s (%d/%d)\n", package, "n/a",
				package_covered[package], package_total[package]
			continue
		}
		percent = 100 * package_covered[package] / package_total[package]
		printf "  %-68s %6.2f%% (%d/%d)\n", package, percent,
			package_covered[package], package_total[package]
		if (100 * package_covered[package] < minimum * package_total[package]) {
			failed = 1
		}
	}

	if (repository_total == 0) {
		printf "Repository coverage: n/a (%d/%d)\n", repository_covered, repository_total
	} else {
		repository_percent = 100 * repository_covered / repository_total
		printf "Repository coverage: %.2f%% (%d/%d; minimum %.2f%%)\n",
			repository_percent, repository_covered, repository_total, minimum
		if (100 * repository_covered < minimum * repository_total) {
			failed = 1
		}
	}

	for (i = 1; i <= file_count; i++) {
		file = file_order[i]
		if (file_total[file] > 0 && file_covered[file] == 0) {
			printf "  untested compiled source file: %s\n", file > "/dev/stderr"
			failed = 1
		}
	}

	if (failed) {
		printf "coverage requirements were not met\n" > "/dev/stderr"
		exit 1
	}
}
