//Reports instruction-stream health metrics used to validate the SN200 Xtensa FLIX decode.
//@category SN200
import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.*;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.*;
import java.util.*;

public class FlixMetrics extends GhidraScript {
	@Override
	public void run() throws Exception {
		Listing l = currentProgram.getListing();

		long nInsn = 0, nFlix = 0, nFp = 0;
		long gapBytes = 0, nGaps = 0;
		Address prevEnd = null;
		Set<Long> starts = new HashSet<>();
		List<Instruction> all = new ArrayList<>();

		for (Instruction i : l.getInstructions(true)) {
			all.add(i);
			starts.add(i.getAddress().getOffset());
			nInsn++;
			String m = i.getMnemonicString().toLowerCase();
			if (m.startsWith("flix")) nFlix++;
			// floating point ops appearing in integer control code = fabricated
			if (m.endsWith(".s") || m.startsWith("add.s") || m.startsWith("mul.s")
					|| m.startsWith("sub.s") || m.startsWith("madd.s")) nFp++;
			if (prevEnd != null && i.getAddress().getOffset() > prevEnd.getOffset()) {
				long g = i.getAddress().getOffset() - prevEnd.getOffset();
				if (g > 0 && g < 64) { gapBytes += g; nGaps++; }
			}
			Address e = i.getMaxAddress();
			if (prevEnd == null || e.getOffset() > prevEnd.getOffset()) prevEnd = e.add(1);
		}

		// branch/call targets that do NOT land on an instruction start
		long nT = 0, badT = 0;
		for (Instruction i : all) {
			for (Reference r : i.getReferencesFrom()) {
				if (!r.getReferenceType().isFlow()) continue;
				Address t = r.getToAddress();
				if (t == null || !t.isMemoryAddress()) continue;
				if (!currentProgram.getMemory().contains(t)) continue;
				nT++;
				if (!starts.contains(t.getOffset())) badT++;
			}
		}

		// gaps INSIDE a function body are desync evidence; gaps between
		// functions are usually just literal pools / alignment padding.
		long inFuncGaps = 0, inFuncGapBytes = 0;
		FunctionIterator fit = currentProgram.getFunctionManager().getFunctions(true);
		while (fit.hasNext()) {
			Function f = fit.next();
			AddressSetView body = f.getBody();
			Address p = null;
			for (Instruction i : l.getInstructions(body, true)) {
				if (p != null && i.getAddress().getOffset() > p.getOffset()) {
					long g = i.getAddress().getOffset() - p.getOffset();
					if (g > 0 && g < 64) { inFuncGaps++; inFuncGapBytes += g; }
				}
				p = i.getMaxAddress().add(1);
			}
		}

		int nFunc = currentProgram.getFunctionManager().getFunctionCount();

		println("METRIC program=" + currentProgram.getName());
		println("METRIC functions=" + nFunc);
		println("METRIC instructions=" + nInsn);
		println("METRIC flix_bundles=" + nFlix);
		println("METRIC fp_instructions=" + nFp);
		println("METRIC addr_gaps=" + nGaps + " gap_bytes=" + gapBytes);
		println("METRIC infunc_gaps=" + inFuncGaps + " infunc_gap_bytes=" + inFuncGapBytes);
		println("METRIC flow_targets=" + nT + " targets_off_boundary=" + badT);
	}
}
