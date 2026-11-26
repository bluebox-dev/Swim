-- Number of WOF (CompactOS) compressed files left in \Windows\System32 and \Windows\Boot of a captured
-- image (must be 0: WinPE runs DISM/bcdboot/drivers from there). Other files may stay compressed.
-- NULL = captured before the builder decompressed them (unknown → not installable by WinPE).
ALTER TABLE images ADD COLUMN IF NOT EXISTS wof_files integer;
