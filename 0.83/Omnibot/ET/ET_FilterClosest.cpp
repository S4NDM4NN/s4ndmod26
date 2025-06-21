#include "PrecompET.h"
#include "ET_FilterClosest.h"
#include "ET_Game.h"

#include "ScriptManager.h"

ET_FilterClosest::ET_FilterClosest(Client *_client, AiState::SensoryMemory::Type _type) :
	FilterClosest				(_client, _type)
{
}

GameEntity GetMountedPlayerOnTank(const MemoryRecord &_record)
{
	if(_record.m_TargetInfo.m_EntityFlags.CheckFlag(ET_ENT_FLAG_MOUNTED))
	{
		static GameEntity lastVehicle, lastPlayer;
		static obint32 nextTime = 0;
		if(lastVehicle == _record.GetEntity() && IGame::GetTime() < nextTime) return lastPlayer;
		nextTime = IGame::GetTime() + 1000; // Don't check again for a second.
		lastVehicle = _record.GetEntity();

		IGame::EntityIterator ent;
		while(IGame::IterateEntity(ent) && ent.GetIndex() < 64)
		{
			BitFlag64 flags;
			Vector3f position;
			EngineFuncs::EntityPosition(ent.GetEnt().m_Entity, position);
			if(EngineFuncs::EntityPosition(ent.GetEnt().m_Entity, position)
				&& SquaredLength(position, _record.GetLastSensedPosition()) < 8000
				&& InterfaceFuncs::GetEntityFlags(ent.GetEnt().m_Entity, flags)
				&& flags.CheckFlag(ET_ENT_FLAG_MNT_TANK) && !flags.CheckFlag(ENT_FLAG_DISABLED)) 
			{
				return lastPlayer = ent.GetEnt().m_Entity;
			}
		}
		lastPlayer.Reset();
	}
	return GameEntity();
}

bool ET_FilterClosest::CheckEx(const MemoryRecord &_record)
{
	// Special consideration for some entity types.
	switch(_record.m_TargetInfo.m_EntityClass - ET_Game::CLASSEXoffset) 
	{
	case ET_CLASSEX_VEHICLE_HVY:
		{
			if(m_Client->GetWeaponSystem()->GetCurrentWeaponID() == ET_WP_MOBILE_MG42_SET) return false;
			GameEntity mounted = GetMountedPlayerOnTank(_record);
			if(mounted.IsValid() && m_Client->IsAllied(mounted)) return false;
			break;
		}
	case ET_CLASSEX_MG42MOUNT:
		{
			GameEntity mounted = InterfaceFuncs::GetMountedPlayerOnMG42(m_Client, _record.GetEntity());
			if(!mounted.IsValid() || m_Client->IsAllied(mounted))
				return false;
			MemoryRecord *record2 = m_Client->GetSensoryMemory()->GetMemoryRecord(mounted);
			if(record2 && record2->ShouldIgnore()) return false;
			break;
		}
	case ET_CLASSEX_BREAKABLE:
		{
			float fBreakableDist = static_cast<ET_Client*>(m_Client)->GetBreakableTargetDist();
			float fDistance = (m_Client->GetPosition() - _record.GetLastSensedPosition()).SquaredLength();
			if(fDistance > (fBreakableDist * fBreakableDist))
				return false;
			break;
		}
	}

	// TODO: Handle disguised covertops
	// cs: done in script for the moment
	//if (_record.m_TargetInfo.m_EntityFlags.CheckFlag(ET_ENT_FLAG_DISGUISED) && Mathf::UnitRandom() > 0.33f)
	//	return false;

	return true;
}

